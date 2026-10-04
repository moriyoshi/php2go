package lower

import (
	"regexp"
	"sort"
	"strings"
)

// LoopBuilder is a string variable that is appended to inside a loop and never
// read there — `$out .= $x` in a `foreach`, the shape that is asymptotically
// wrong against immutable Go strings and that the engine gets right by mutating
// in place at refcount 1.
//
// Unlike the rope and array-literal builders, uniqueness here is *not* in the
// encoding: `$out` is a program variable, so it can be aliased by `&`, captured
// by a closure, bound to a global or a static, or reached through a variable
// variable. Every one of those has to be excluded before the buffer is safe,
// which is why this is an analysis and the structural half was not.
type LoopBuilder struct {
	Name string
	// Appends is how many `.=` sites are inside a loop.
	Appends int
	// ReadsOutside is how many places the finished string is read. Each is a
	// point where a promoted buffer would have to be materialised.
	ReadsOutside int
}

// Reject is a variable appended to inside a loop that cannot be promoted, and
// the reason. The reasons matter more than the count: they say whether the
// remaining work is analysis or something the language makes impossible.
type Reject struct {
	Name string
	Why  string
	// Soft marks a refusal that comes from the buffer representation rather
	// than from the program. A read partway through the build is only fatal if
	// reading the buffer destroys it or costs a copy; Go's strings.Builder
	// snapshots in O(1) and appends only ever write past the length already
	// handed out, so a Go target could promote these. Verified by running it.
	//
	// A hard refusal is about aliasing and no target choice can rescue it.
	Soft bool
}

// The instructions that make a place reachable other than by name. Any of these
// touching a candidate means its contents can change or be observed somewhere
// this function cannot see.
var placeEscapes = map[string]string{
	"bind_ref":      "aliased by reference",
	"bind_ref.loop": "bound by a by-reference foreach",
	"make_ref":      "a reference was taken",
	"bind_global":   "bound to a global",
	"bind_static":   "bound to a static",
	"bind_lexical":  "captured by a closure",
	"unset":         "unset",
}

// Functions that can read or create locals by computed name, which makes every
// variable in the frame reachable without naming it.
var frameOpaque = map[string]bool{
	`"extract"`:          true,
	`"compact"`:          true,
	`"get_defined_vars"`: true,
}

// readInLoop is the one refusal that is not about aliasing.
const readInLoop = "read inside the loop"

var byRefArg = regexp.MustCompile(`(?:byref|preferref) (\$[A-Za-z_][A-Za-z0-9_]*)`)

// FindLoopBuilders reports the variables that could be carried as a mutable
// buffer across a loop, and the ones that could not with the reason why.
//
// It is deliberately one-sided: anything it cannot prove, it rejects. A missed
// promotion costs speed, a wrong one changes what the program prints.
func FindLoopBuilders(fn *Function) (found []LoopBuilder, rejected []Reject) {
	cfg := Analyze(fn)
	loop := cfg.InCycle()
	if why := frameIsOpaque(fn); why != "" {
		// Nothing in this frame can be promoted, and saying so once is more
		// useful than repeating it per variable.
		for name := range appendedInLoop(fn, loop) {
			rejected = append(rejected, Reject{name, why, false})
		}
		sortRejects(rejected)
		return nil, rejected
	}

	candidates := appendedInLoop(fn, loop)
	if len(candidates) == 0 {
		return nil, nil
	}

	// One pass over the function classifying every mention of every candidate.
	type state struct {
		appends      int
		readsOutside int
		why          string
	}
	st := map[string]*state{}
	for name := range candidates {
		st[name] = &state{}
	}
	note := func(name, why string) {
		if s := st[name]; s != nil && s.why == "" {
			s.why = why
		}
	}

	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		compound := compoundAssigns(b)
		accumulatorRead := map[int]bool{}
		for _, c := range compound {
			accumulatorRead[c.read] = true
		}
		for i, in := range b.Insts {
			if in.Directive() || accumulatorRead[i] {
				continue
			}
			// The append half of a compound assignment. `$out .= $x` is a read,
			// an intrinsic call and a write, so without this the call's first
			// argument reads as an ordinary read inside the loop and every
			// candidate is refused. Only the first argument is the accumulator:
			// `$out .= $out` really does read it.
			if c, isCompound := compound[i]; isCompound {
				if s := st[c.place]; s != nil {
					if c.intrinsic == "Concat" {
						if loop[b.ID] {
							s.appends++
						}
					} else {
						// `$s .= x` is a builder; `$s += x` on the same
						// variable is not, and mixing them means it is not a
						// string.
						note(c.place, "compound-assigned with an operator other than concat")
					}
				}
				for _, a := range CallArgs(in)[1:] {
					if s := st[a]; s != nil {
						if loop[b.ID] {
							note(a, readInLoop)
						} else {
							s.readsOutside++
						}
					}
				}
				continue
			}
			if why, isEscape := placeEscapes[in.Opcode]; isEscape {
				for _, operand := range in.Operands {
					if st[operand] != nil {
						note(operand, why)
					}
				}
			}
			for _, m := range byRefArg.FindAllStringSubmatch(in.ArgList, -1) {
				if st[m[1]] != nil {
					note(m[1], "passed by reference")
				}
			}
			// A store: its **place** is written, not read -- this is where a
			// buffer would be created -- while its value is an ordinary use.
			// Handled before the generic scans because a store's place now
			// lives in the argument list, where those scans see a mention and
			// call it a read. That turned every aliasing refusal into "read
			// inside the loop", which is a true statement about the wrong
			// instruction.
			if place, value, isStore := StoreOf(in); isStore {
				if s := st[value]; s != nil {
					if loop[b.ID] {
						note(value, readInLoop)
					} else {
						s.readsOutside++
					}
				}
				_ = place
				continue
			}
			for _, operand := range in.Operands {
				s := st[operand]
				if s == nil {
					continue
				}
				if loop[b.ID] {
					note(operand, "read inside the loop")
				} else {
					s.readsOutside++
				}
			}
			// A call argument mentions the variable by name in the arg list
			// rather than in Operands, so plain by-value uses are counted here.
			if in.ArgList != "" {
				for name, s := range st {
					if s.why != "" || !mentions(in.ArgList, name) {
						continue
					}
					if loop[b.ID] {
						note(name, "read inside the loop")
					} else {
						s.readsOutside++
					}
				}
			}
		}
		// A terminator reading the variable is a read like any other.
		for name, s := range st {
			if s.why != "" || !mentions(b.Term, name) {
				continue
			}
			if loop[b.ID] {
				note(name, readInLoop)
			} else {
				s.readsOutside++
			}
		}
	}

	for name, s := range st {
		switch {
		case s.why != "":
			rejected = append(rejected, Reject{name, s.why, s.why == readInLoop})
		case s.appends == 0:
			rejected = append(rejected, Reject{name, "no append survives inside a loop", false})
		default:
			found = append(found, LoopBuilder{name, s.appends, s.readsOutside})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	sortRejects(rejected)
	return found, rejected
}

func sortRejects(r []Reject) {
	sort.Slice(r, func(i, j int) bool {
		if r[i].Name != r[j].Name {
			return r[i].Name < r[j].Name
		}
		return r[i].Why < r[j].Why
	})
}

// mentions reports whether a rendered operand list names this place. Matching
// on the whole token matters: `$s` must not match inside `$sum`.
func mentions(text, name string) bool {
	for i := 0; i+len(name) <= len(text); i++ {
		if text[i:i+len(name)] != name {
			continue
		}
		if j := i + len(name); j < len(text) && isNameByte(text[j]) {
			continue
		}
		return true
	}
	return false
}

func isNameByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// frameIsOpaque reports whether this function can reach its own locals without
// naming them, which makes every variable in it unpromotable. `$$x` lowers to a
// fetch whose name operand is a value rather than a literal place.
func frameIsOpaque(fn *Function) string {
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			// A dynamic variable reaches every local in the frame without
			// naming one, so nothing here can be promoted. The lowering gives
			// these their own opcode precisely so this check is possible: they
			// used to render as `local.get $name`, indistinguishable from
			// reading a variable actually called `$name`.
			if strings.HasPrefix(in.Opcode, "dynvar.") && !hasAttr(in, "scope=global") {
				return "the frame uses variable variables"
			}
			// `isset($$n)` reaches every local by name too, and it is the only
			// thing some bodies do with one -- so a frame whose *only* dynamic
			// access is an isset was promoted, and the slot the name was about
			// to reach had been optimised away. A global one names another
			// table and leaves this frame alone.
			if in.Opcode == "isset" || in.Opcode == "empty" {
				if hasAttr(in, "by=name") && !hasAttr(in, "scope=global") {
					return "the frame uses variable variables"
				}
			}
			// An included file reads and writes this frame's variables by
			// name, as extract does.
			if in.Opcode == "include" {
				return "the frame includes a file"
			}
			if in.Opcode == "call.fn" && len(in.Operands) > 0 && frameOpaque[in.Operands[0]] {
				return "the frame calls " + strings.Trim(in.Operands[0], `"`)
			}
		}
	}
	return ""
}

// appendedInLoop collects the program variables that are `.=`-appended inside a
// loop. Compiler temporaries are excluded: those are the structural builders,
// which need no analysis at all.
func appendedInLoop(fn *Function, loop []bool) map[string]bool {
	out := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead || !loop[b.ID] {
			continue
		}
		for _, c := range compoundAssigns(b) {
			if c.intrinsic != "Concat" {
				continue
			}
			if strings.HasPrefix(c.place, "$") && !strings.HasPrefix(c.place, "$__") {
				out[c.place] = true
			}
		}
	}
	return out
}

// compound is one recovered compound assignment: the place it reads and writes,
// and the intrinsic in between.
type compound struct {
	place     string
	intrinsic string
	// read is the index of the instruction that read the accumulator for this
	// operation. It is part of the compound assignment, not a use of the
	// variable, so the scan must skip it -- otherwise `$out .= $x` inside a
	// loop refuses itself as "read inside the loop" and no promotion survives.
	read int
}

// compoundAssigns recovers `$p op= v` from the read-call-write triple it lowers
// to, keyed by the index of the call.
//
// The shape is exact, not approximate: the call's first argument must be the
// place, and the very next instruction must be a `local.set` of that same place
// from the call's own result. Anything else is an ordinary call whose result
// happens to be assigned, which is not a compound assignment and must not be
// promoted to a builder.
func compoundAssigns(b *Block) map[int]compound {
	out := map[int]compound{}
	for i, in := range b.Insts {
		if in.Opcode != IntrinsicCall || in.Result == nil {
			continue
		}
		args := CallArgs(in)
		if len(args) == 0 {
			continue
		}
		// The accumulator reaches the operation as the **value its read
		// produced**, since reads are explicit instructions. Following that one
		// hop is what keeps `$out .= $x` recognisable; matching on `$out` in
		// the argument stopped matching the moment reads became calls, and
		// every promotion was refused.
		place, readAt, ok := readSource(b, args[0])
		if !ok {
			continue
		}
		j := i + 1
		for j < len(b.Insts) && b.Insts[j].Directive() {
			j++
		}
		if j >= len(b.Insts) {
			continue
		}
		wp, wv, isStore := StoreOf(b.Insts[j])
		if !isStore || wp != place || wv != in.Result.String() {
			continue
		}
		sig, _, ok := IntrinsicOf(in)
		if !ok {
			continue
		}
		// Only an **operator** makes a compound assignment. The shape
		// "read a place, call something, write it back" also fits the return
		// coercion -- `$out = coerce_return($out)` -- which is not `$out op= v`
		// and which this refused as "compound-assigned with an operator other
		// than concat", killing the promotion.
		if sig.Group != "arithmetic" && sig.Group != "bitwise" {
			continue
		}
		out[i] = compound{place: place, intrinsic: sig.Name, read: readAt}
	}
	return out
}

// readSource is the variable an SSA value was read from, for a value produced by
// the read intrinsic in this block.
func readSource(b *Block, operand string) (string, int, bool) {
	operand = strings.TrimSpace(operand)
	if !strings.HasPrefix(operand, "%") {
		return "", -1, false
	}
	for i, in := range b.Insts {
		if in.Result == nil || in.Result.String() != operand {
			continue
		}
		if _, v, ok := IntrinsicOf(in); !ok || (v.Name != "read_var" && v.Name != "read_var.silent") {
			return "", -1, false
		}
		args := CallArgs(in)
		if len(args) == 0 || !strings.HasPrefix(args[0], "$") {
			return "", -1, false
		}
		return args[0], i, true
	}
	return "", -1, false
}

// isStoreInst reports whether an instruction writes a place, in any of the
// spellings a store wears.
func isStoreInst(in Inst) bool {
	_, _, ok := StoreOf(in)
	return ok
}
