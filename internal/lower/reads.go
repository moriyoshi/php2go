package lower

import (
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// Making reads explicit.
//
// # A read is an operation, and the IR should say so
//
// Zend's operands are CV, TMP and VAR. Lowering maps TMP and VAR to SSA values
// but carried a CV through **as its place name**, so `$a + $b` read as
//
//	%1:zval = call.intrinsic "add"($a, $b)
//
// with the two reads nowhere in the IR. They are not free: reading a variable
// warns when it is unset and dereferences a reference when it is bound, and the
// emitter did both inside its operand renderer. That is the same
// decoration-instead-of-a-call shape the operator family had, one level down.
//
// After this pass:
//
//	%1:zval = call.intrinsic "read_var"($a, "a")
//	%2:zval = call.intrinsic "read_var"($b, "b")
//	%3:zval = call.intrinsic "add"(%1, %2)
//
// Four things that were awkward become ordinary:
//
//   - The undefined-variable warning is an instruction with a position, so a
//     consumer cannot emit it twice or in the wrong order by accident.
//   - The silent form is a *different intrinsic* rather than a flag every
//     consumer re-reads: `read_var.silent` is pure, `read_var` warns.
//   - Type inference stops special-casing places. A place's type reaches an
//     expression through an ordinary SSA value.
//   - Copy elision has somewhere to ask its question. "Is the source dead after
//     this point" needs a point, and an implicit read has none.
//
// # What is not a read
//
// A place used as a **target** is not read: `local.set $a, %1` writes it,
// `bind_ref $a, $b` rebinds it, `unset $a` removes it, and `dim.ref $a, 0`
// takes its address. Getting this wrong would invent a warning on `$a[0] = 1`
// for an unset `$a`, which autovivifies silently.
//
// Compiler temporaries -- `$__t3`, `$__iter5` -- are not read either. They are
// written before they are read by construction and the program cannot name
// them, so warning about one would invent a diagnostic PHP never emits.

// ExplicitReads rewrites every read of a program variable into a call to the
// read intrinsic. `Lower` runs it, after IntrinsicCalls.
func ExplicitReads(fn *Function) {
	for _, b := range fn.Blocks {
		out := make([]Inst, 0, len(b.Insts))
		for _, in := range b.Insts {
			var reads []Inst
			operandAt := -1
			read := func(operand string) string {
				if _, ok := readableVar(operand); !ok {
					return operand
				}
				v := fn.newValue("zval")
				variant := "read_var"
				if silentReads(in) && !loudValueOperand(in, operandAt) {
					variant = "read_var.silent"
				}
				// The place alone: `$a` is both the slot and its name, and the
				// emitter renders whichever the binding needs.
				args := "(" + operand + ")"
				reads = append(reads, Inst{
					Result: &v, Opcode: IntrinsicCall,
					Operands: []string{strconv.Quote(variant)}, ArgList: args,
				})
				return v.String()
			}

			for i := range in.Operands {
				if valuePosition(in, i) {
					in.Operands[i] = read(in.Operands[i])
				}
			}
			if args, shapes, ok := callArgShapes(in); ok {
				for i := range args {
					if shapeReads(shapes, i) {
						operandAt = i
						args[i] = read(args[i])
					}
				}
				operandAt = -1
				in.ArgList = "(" + strings.Join(args, ", ") + ")"
			} else if in.ArgList != "" {
				in.ArgList = readSendArgs(in.ArgList, read)
			}

			out = append(out, reads...)
			out = append(out, in)
		}
		// The terminator reads too: `ret $sum` and `br $c, bb1, bb2`. Its reads
		// go at the end of the block, which is where they happen.
		// `ret.ref $x` **takes the address** and does not read: `function &f()
		// { return $undef; }` binds the caller to the slot and creates it,
		// with no undefined-variable warning -- measured. Reading it here lost
		// the box as well as inventing the warning, so `global $g; return $g;`
		// handed back a copy and the caller's `$r =& k()` bound to nothing.
		// This is the terminator's entry in the "what is not a read" list the
		// doc comment above already keeps for instructions.
		if b.Term != "" && !strings.HasPrefix(b.Term, "ret.ref ") {
			var reads []Inst
			b.Term = rewriteTermVars(b.Term, func(operand string) string {
				if _, ok := readableVar(operand); !ok {
					return operand
				}
				v := fn.newValue("zval")
				reads = append(reads, Inst{
					Result: &v, Opcode: IntrinsicCall,
					Operands: []string{strconv.Quote("read_var")},
					ArgList:  "(" + operand + ")",
				})
				return v.String()
			})
			out = append(out, reads...)
		}
		b.Insts = out
	}
}

// readableVar reports whether an operand is a program variable being read, and
// its name. A compiler temporary is not one: it is written before it is read by
// construction and the program cannot name it, so a warning about it would be a
// diagnostic PHP never emits.
// ReadableVar is readableVar for the emitter, which asks the same question
// about the same spelling: is this operand a PHP variable whose name a
// diagnostic can print.
func ReadableVar(operand string) (string, bool) { return readableVar(operand) }

func readableVar(operand string) (string, bool) {
	operand = strings.TrimSpace(operand)
	if !strings.HasPrefix(operand, "$") || strings.HasPrefix(operand, "$__") {
		return "", false
	}
	name := operand[1:]
	if name == "" || name == "this" {
		return "", false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			// A name the place scanner cannot spell is refused by the emitter;
			// reading it here would only move where that happens.
			return "", false
		}
	}
	return name, true
}

// placeTargets are the operand positions that hold a place being **written**,
// bound or addressed rather than read. Absent means every position is a read.
var placeTargets = map[string]map[int]bool{
	// This pass runs after IntrinsicCalls, so a store is a `write_var` call or
	// a `slot.set`; `local.set` cannot reach here.
	SlotSet:            {0: true},
	"store":            {0: true},
	"bind_ref":         {0: true, 1: true},
	"bind_global":      {0: true},
	"bind_lexical":     {0: true, 1: true},
	"bind_static":      {0: true},
	"unset":            {0: true},
	"dynvar.ref":       {0: true},
	"place.get":        {0: true},
	"recv":             {0: true},
	"iter.new":         {0: true},
	"iter.current":     {0: true},
	"iter.key":         {0: true},
	"iter.next":        {0: true},
	"iter.free":        {0: true},
	"declare_class":    {0: true},
	"declare_function": {0: true},
	"declare_const":    {0: true},
	"instanceof":       {1: true},
	"new":              {0: true},
	"clone":            {},
	"verify_return":    {},
	"throw":            {},
	"dynvar.get":       {},
	"call.ctor":        {},
	// The receiver is a value: `$obj->m()` reads `$obj`. `$this` is excluded by
	// readableVar, because it is the method's own receiver rather than a slot.
	"call.method":   {},
	"call.dynamic":  {},
	"fiber.suspend": {},
	"silence_end":   {0: true},
	"strbuf.new":    {},
}

// valuePosition reports whether operand i of this instruction is read for its
// value. An intrinsic call's arguments live in ArgList, so its Operands are the
// callee name alone and never a read.
func valuePosition(in Inst, i int) bool {
	if in.Opcode == IntrinsicCall || in.Directive() {
		return false
	}
	if targets, known := placeTargets[in.Opcode]; known {
		return !targets[i]
	}
	// An unknown opcode's operands are treated as places, not values: adding a
	// read where none belongs invents a warning, while missing one leaves the
	// behaviour exactly as it was. Failing closed is the safe direction.
	return false
}

// callArgShapes returns an intrinsic call's arguments and the shapes the
// catalogue gives them.
func callArgShapes(in Inst) ([]string, []string, bool) {
	_, v, ok := IntrinsicOf(in)
	if !ok {
		return nil, nil, false
	}
	return CallArgs(in), v.Args, true
}

// shapeReads reports whether an argument shape is a value the callee reads.
// Absent shapes default to a plain value, which is a read.
func shapeReads(shapes []string, i int) bool {
	if i >= len(shapes) {
		return true
	}
	switch shapes[i] {
	case "", "obj", "read":
		return true
	}
	return false
}

// readSendArgs rewrites the value-reading arguments of a PHP call. `byref`
// passes the box and `preferref` is undecided until the callee is known, so
// neither is a read here.
//
// **`unpack` is a read**, and leaving it out was a bug with a loud symptom:
// `call_user_func_array($cb, $args)` and `f(...$args)` both compile to an
// unpack send, so the argument stayed a bare place that nothing read. The local
// was then promoted away as unused and the call said `Undefined variable $args`
// -- inside a body whose `var_dump($args)` one line earlier printed it fine.
func readSendArgs(list string, read func(string) string) string {
	inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(list), "("), ")")
	parts := splitCallArgs(inner)
	for i, a := range parts {
		trimmed := strings.TrimSpace(a)
		mode := "byval "
		rest, isVal := strings.CutPrefix(trimmed, mode)
		if !isVal {
			mode = "unpack "
			rest, isVal = strings.CutPrefix(trimmed, mode)
		}
		if !isVal {
			continue
		}
		if _, _, isNamed := CutNamedSend(rest); isNamed {
			continue
		}
		parts[i] = mode + read(strings.TrimSpace(rest))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// rewriteTermVars applies a rewrite to each `$name` in a terminator. The
// terminator is text rather than operands, so this is the one place that has to
// scan it.
//
// **A `$` inside a string literal is text**, and skipping the literal is what
// keeps this agreeing with `PlaceNames`, which masks quoted regions before it
// scans. They disagreed: `return 'B::$prop::get()'` lowers to a terminator
// carrying that literal, the rewriter substituted the promoted local, and the
// program printed `B::%0::get()` with an `Undefined variable $prop` warning
// beside it. A scanner and a rewriter over the same text that do not agree
// about what a token is will corrupt whatever only one of them sees.
func rewriteTermVars(term string, rewrite func(string) string) string {
	var out strings.Builder
	for i := 0; i < len(term); {
		if term[i] == '"' {
			// Copy the literal whole, honouring backslash escapes so a `\"`
			// inside it does not look like the end.
			j := i + 1
			for j < len(term) && term[j] != '"' {
				if term[j] == '\\' && j+1 < len(term) {
					j++
				}
				j++
			}
			if j < len(term) {
				j++
			}
			out.WriteString(term[i:j])
			i = j
			continue
		}
		if term[i] != '$' {
			out.WriteByte(term[i])
			i++
			continue
		}
		j := i + 1
		for j < len(term) && (term[j] == '_' ||
			term[j] >= 'a' && term[j] <= 'z' ||
			term[j] >= 'A' && term[j] <= 'Z' ||
			term[j] >= '0' && term[j] <= '9') {
			j++
		}
		out.WriteString(rewrite(term[i:j]))
		i = j
	}
	return out.String()
}

// hasAttr reports whether an instruction carries an attribute.
// silentReads reports whether an instruction's place operands are read without
// the undefined-variable warning. **The intrinsic says so**, rather than this
// pass re-deriving it from a `mode=IS` attribute the lowering happened to
// leave behind: the mode is spent when the opcode becomes a call, and a fact
// two passes scan for independently is a fact that drifts.
// loudValueOperand is an argument a silently-reading intrinsic still reads
// **loudly**: `prop.set`'s silence is for its receiver -- a write fetch does
// not warn about the object it writes into -- and not for the value being
// assigned, which `$o->$p = $v` warns about for an undefined `$v`, before the
// name (probed, gh10251).
func loudValueOperand(in Inst, arg int) bool {
	if arg < 0 || in.Opcode != IntrinsicCall || len(in.Operands) == 0 {
		return false
	}
	// prop.set(recv, name, value): the value is the third argument.
	return strings.Trim(in.Operands[0], `"`) == "prop.set" && arg == 2
}

func silentReads(in Inst) bool {
	// An explicit `silent` attr overrides the variant's own answer. The `?->`
	// guard is the case: `is_set` warns about its operand in a bare chain and
	// must not in an isset or `??` one, which is a property of the *chain* and
	// not of the intrinsic.
	if hasAttr(in, "silent") {
		return true
	}
	if in.Opcode != IntrinsicCall || len(in.Operands) == 0 {
		return false
	}
	v, ok := php.LookupVariant(strings.Trim(in.Operands[0], `"`))
	return ok && v.SilentReads
}

func hasAttr(in Inst, attr string) bool {
	for _, a := range in.Attrs {
		if a == attr {
			return true
		}
	}
	return false
}

// CutNamedSend splits `byval %0 as "b"` into the send and the name it arrived
// under.
//
// The suffix is found by **parsing**, not by searching for ` as `: an operand
// may be a string literal that contains it, and `var_dump("thrown as given")`
// was refused as a named argument for exactly that reason. The name is the
// whole of what follows, so a candidate counts only when the quoted string it
// opens runs to the very end of the text.
func CutNamedSend(a string) (head, name string, named bool) {
	a = strings.TrimSpace(a)
	if !strings.HasSuffix(a, `"`) {
		return a, "", false
	}
	for i := 0; i+5 <= len(a); i++ {
		if !strings.HasPrefix(a[i:], ` as "`) {
			continue
		}
		if quoted, ok := quotedToEnd(a[i+4:]); ok {
			return strings.TrimSpace(a[:i]), quoted, true
		}
	}
	return a, "", false
}

// quotedToEnd reads one quoted string and reports whether it ends the text.
func quotedToEnd(text string) (string, bool) {
	if len(text) < 2 || text[0] != '"' {
		return "", false
	}
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '"':
			if i != len(text)-1 {
				return "", false
			}
			unquoted, err := strconv.Unquote(text[:i+1])
			if err != nil {
				return "", false
			}
			return unquoted, true
		}
	}
	return "", false
}
