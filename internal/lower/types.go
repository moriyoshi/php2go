package lower

import (
	"fmt"
	"strconv"
	"strings"
)

// World is the corpus-wide declaration table the receiver inference reads.
//
// Everything in it is *declared*, not inferred: parameter and return types from
// signatures, property types from class bodies, and the class hierarchy. That is
// deliberate for a first pass — the census says declarations alone determine the
// receiver at four fifths of method call sites, and dataflow is needed for the
// remaining fifth.
type World struct {
	// Returns maps `f` or `c::m` to its declared return type.
	Returns map[string]string
	// Props maps `c::p` to its declared type.
	Props map[string]string
	// Parent maps a class to the one it extends.
	Parent map[string]string
	// children is the inverse, built by Close.
	children map[string][]string
	// Generators names the functions and methods whose *call* makes a Generator
	// object rather than running the body. Keyed like Returns.
	//
	// It is not derivable from a declared return type: `function gen() { yield
	// 1; }` declares nothing, and the fact lives in the body. So it is supplied
	// by whoever lowered the bodies.
	Generators map[string]bool
}

func NewWorld() *World {
	return &World{
		Returns:    map[string]string{},
		Props:      map[string]string{},
		Parent:     map[string]string{},
		Generators: map[string]bool{},
	}
}

// Close builds the derived indexes. Call once every declaration is in.
func (w *World) Close() {
	w.children = map[string][]string{}
	for c, p := range w.Parent {
		if p != "" {
			w.children[p] = append(w.children[p], c)
		}
	}
}

// Descendants returns a class and everything extending it, transitively.
//
// A receiver declared `Foo` can hold a `Bar extends Foo`, and `Bar::m` may
// override. Narrowing a call to `Foo::m` alone would be **unsound** — it is the
// class hierarchy that makes narrowing safe, not the declared type by itself.
// The visited set is not defensive tidiness. A corpus is not necessarily one
// program: `Zend/tests` has hundreds of independent files each declaring their
// own `class A extends B`, and a parent map keyed by name alone merges them, so
// `a` extends `b` in one file and `b` extends `a` in another. Walking that
// without a guard appends for ever -- it reached 119 GB resident before this.
func (w *World) Descendants(class string) []string {
	class = strings.ToLower(class)
	seen := map[string]bool{class: true}
	out := []string{class}
	for i := 0; i < len(out); i++ {
		for _, c := range w.children[out[i]] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// classOf reduces a declared type to a class name, or "" when it is not one.
// Scalars, `mixed`, `void` and unions carry no class; a nullable `?Foo` does.
func classOf(decl string) string {
	d := strings.TrimPrefix(strings.TrimSpace(decl), "?")
	if d == "" || strings.ContainsAny(d, "|&") {
		return ""
	}
	switch strings.ToLower(d) {
	case "array", "string", "int", "float", "bool", "mixed", "void", "null",
		"callable", "iterable", "object", "never", "false", "true", "static", "parent":
		return ""
	}
	return d
}

// InferReceivers annotates every `call.method` whose receiver's class can be
// determined, as `recv="C"`. Annotating rather than returning a side table keeps
// the result visible in the printed IR, diffable, and available to any consumer
// without re-deriving it.
//
// Returns how many sites it resolved and how many it saw.
func InferReceivers(fn *Function, class string, w *World) (resolved, total int) {
	// A place's class, from the signature. Parameters are the only places this
	// pass types; anything assigned in the body needs dataflow.
	place := map[string]string{}
	for _, p := range fn.Params {
		// `0 $rows "array"` — index, place, declared type.
		fields := strings.SplitN(p, " ", 3)
		if len(fields) < 3 {
			continue
		}
		decl, err := strconv.Unquote(fields[2])
		if err != nil {
			continue
		}
		if c := classOf(decl); c != "" {
			// A by-reference parameter is spelled `&$x` and names the place
			// `$x`; keying on the spelling would lose every one of them.
			place[strings.TrimPrefix(fields[1], "&")] = c
		}
	}

	value := map[string]string{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for i := range b.Insts {
			in := &b.Insts[i]
			// The receiver is annotated *before* the result class is recorded,
			// so that a chain -- `$this->a()->b()` -- can read the annotation
			// this pass just made when it types the next link.
			if in.Opcode == "call.method" && len(in.Operands) >= 2 {
				total++
				recv := in.Operands[0]
				var c string
				switch {
				case recv == "$this":
					// The enclosing class, which needs no analysis whatever and
					// is 43.9% of CakePHP's method calls on its own.
					c = class
				case strings.HasPrefix(recv, "%"):
					c = value[recv]
				default:
					c = place[recv]
				}
				if c != "" {
					resolved++
					in.Attrs = append(in.Attrs, fmt.Sprintf("recv=%q", c))
				}
			}
			if in.Result != nil {
				if c := resultClass(*in, class, place, value, w); c != "" {
					value[in.Result.String()] = c
				}
			}
		}
	}
	return resolved, total
}

// resultClass is the class an instruction's result holds, where a declaration
// says so. No dataflow: only `new`, a typed property read, and a call whose
// callee declares a class return type.
func resultClass(in Inst, class string, place, value map[string]string, w *World) string {
	lit := func(s string) string {
		v, err := strconv.Unquote(s)
		if err != nil {
			return ""
		}
		return v
	}
	switch in.Opcode {
	case "new":
		if len(in.Operands) > 0 {
			return classOf(lit(in.Operands[0]))
		}
	case "this":
		return class
	case "prop.get":
		if len(in.Operands) < 2 {
			return ""
		}
		owner := ""
		switch {
		case in.Operands[0] == "$this":
			owner = class
		case strings.HasPrefix(in.Operands[0], "%"):
			owner = value[in.Operands[0]]
		default:
			owner = place[in.Operands[0]]
		}
		if owner == "" {
			return ""
		}
		return classOf(w.Props[strings.ToLower(owner+"::"+lit(in.Operands[1]))])
	case "call.fn":
		if len(in.Operands) > 0 {
			name := strings.ToLower(lit(in.Operands[0]))
			// **Calling a generator function makes a Generator**, whatever it
			// declares -- the fact is in the body, not in a return type. It is
			// worth typing because the result is then a receiver whose driver
			// calls can be recognised: `$g = gen(); $g->next();`.
			if w.Generators[name] {
				return "Generator"
			}
			return classOf(w.Returns[name])
		}
	case "call.static":
		if len(in.Operands) > 1 {
			key := strings.ToLower(lit(in.Operands[0]) + "::" + lit(in.Operands[1]))
			if w.Generators[key] {
				return "Generator"
			}
			return classOf(w.Returns[key])
		}
	case "call.method":
		if len(in.Operands) < 2 {
			return ""
		}
		// Only when this receiver was itself resolved -- which is why the
		// annotation is applied in instruction order.
		for _, a := range in.Attrs {
			if !strings.HasPrefix(a, "recv=") {
				continue
			}
			owner := lit(strings.TrimPrefix(a, "recv="))
			return classOf(w.Returns[strings.ToLower(owner+"::"+lit(in.Operands[1]))])
		}
	}
	return ""
}
