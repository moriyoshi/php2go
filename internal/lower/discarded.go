package lower

import (
	"strconv"
	"strings"
)

// Objects made and then used by nothing at all.
//
//	new foo;                 // a bare statement
//	class_alias(...);
//	var_dump(new foo);       // the engine prints #1 for BOTH
//
// The engine frees a statement-level temporary at the end of its statement, so
// the second `new` reuses the first's handle. Neither existing analysis claims
// this: `ConfinedObjects` asks whether an object outlives its *frame*, and is
// skipped for the file body because `{main}`'s locals are globals, while
// `DisposableTemporaries` claims a value that some non-retaining call consumes.
// A value consumed by **nothing** falls between them, and it is the commonest
// shape left in the handle-numbering failures.

// DiscardedObjects answers the `new` results whose only use is their own
// constructor, so the object is dead the moment that constructor returns.
//
// `ctorLeaks` reports whether a class's constructor stores `$this`; one that
// does has put the object somewhere this cannot see, and the site is refused.
// A `new` whose class is not a literal -- `new $var` -- is refused for the same
// reason: there is no constructor to ask about.
func DiscardedObjects(fn *Function, ctorLeaks func(class string) bool) map[string]bool {
	if fn == nil || ctorLeaks == nil {
		return nil
	}
	// **No opacity refusal.** extract, compact, get_defined_vars, eval, an
	// include and a variable variable all reach a frame's *variables* by
	// name; a value claimed here is in none -- nothing but its constructor
	// ever mentions it -- so none of them can reach it. Refusing the body
	// over them left `new Destructor();` beside an eval to run its
	// destructor at script end (attributes/deprecated/functions/
	// throwing_error_handler_001).
	// Every `new` with a literal class, and the value it produced.
	candidates := map[string]string{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode != "new" || in.Result == nil || len(in.Operands) == 0 {
				continue
			}
			class := strings.TrimSpace(in.Operands[0])
			if !strings.HasPrefix(class, `"`) {
				continue // `new $var`: no constructor to ask about
			}
			candidates[in.Result.String()] = strings.Trim(class, `"`)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	// Any use other than the value's own `new` and its own `call.ctor` keeps
	// it alive. Counting uses rather than recognising them: this claim is that
	// nothing at all looks at the object, so an unrecognised use is a use.
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			for v := range candidates {
				if in.Result != nil && in.Result.String() == v && in.Opcode == "new" {
					continue
				}
				if in.Opcode == "call.ctor" && len(in.Operands) > 0 &&
					escapeToken(in.Operands[0], v) &&
					!escapeMentions(escapeArgs(in.ArgList), v) {
					continue
				}
				if escapeMentions(in.Operands, v) ||
					escapeMentions(escapeArgs(in.ArgList), v) ||
					(in.Result != nil && in.Result.String() == v) {
					delete(candidates, v)
				}
			}
		}
		for v := range candidates {
			if escapeToken(b.Term, v) {
				delete(candidates, v)
			}
		}
	}
	out := map[string]bool{}
	for v, class := range candidates {
		// A value that stands for a promoted local is held by that local
		// (mem2reg's FrameBound), whatever the instructions say.
		if ctorLeaks(strings.ToLower(class)) || fn.FrameBound[v] {
			continue
		}
		out[v] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// LoopSubjectAttr is the attribute an `iter.free.owned` carries naming the
// value its loop iterated, when that was an SSA value.
const LoopSubjectAttr = "subject="

// LoopOwnedObjects answers the `new` results a `foreach` holds alone and frees:
//
//	foreach (new A as $v) { ... }
//
// The cursor is the object's only holder, so the engine destroys it at the
// loop's FE_FREE -- a `break`, a `return` from inside, or falling out of the
// loop -- and its destructor runs there, before whatever follows. Neither
// ConfinedObjects nor DiscardedObjects claims it: the iterator reset is a use
// the first does not recognise and the second counts.
//
// Claimed only when the object is used by nothing but its own `new`, its
// constructor, and the one iterator reset, and when `plain` says iterating it
// cannot run code of the class's: iterating an Iterator or an
// IteratorAggregate calls its methods with `$this`, and a hooked property's
// get hook runs too, so any of those could keep it. `ctorLeaks` is asked as
// DiscardedObjects asks it. Keyed by the `new` result.
func LoopOwnedObjects(fn *Function, ctorLeaks, plain func(class string) bool) map[string]bool {
	if fn == nil || ctorLeaks == nil || plain == nil {
		return nil
	}
	text := fn.String()
	if strings.Contains(text, "dynvar") {
		return nil
	}
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	freed := map[string]bool{}
	// The CPS slot a subject was demoted to, when the loop's body suspends:
	// `$__cpsN` holds `%N` from its definition on (demoteLiveAcross), so the
	// loop's end frees `%N` through it.
	viaSlot := map[string]string{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if subject, ok := loopSubject(in); ok {
				if n, cps := strings.CutPrefix(subject, "$__cps"); cps {
					viaSlot["%"+n] = subject
					subject = "%" + n
				}
				freed[subject] = true
			}
		}
	}
	if len(freed) == 0 {
		return nil
	}
	candidates := map[string]string{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode != "new" || in.Result == nil || len(in.Operands) == 0 ||
				!freed[in.Result.String()] {
				continue
			}
			class := strings.TrimSpace(in.Operands[0])
			if !strings.HasPrefix(class, `"`) {
				continue
			}
			candidates[in.Result.String()] = strings.ToLower(strings.Trim(class, `"`))
		}
	}
	resets := map[string]int{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			for v := range candidates {
				switch {
				case in.Result != nil && in.Result.String() == v && in.Opcode == "new":
					continue
				case in.Opcode == "call.ctor" && len(in.Operands) > 0 &&
					escapeToken(in.Operands[0], v) &&
					!escapeMentions(in.Operands[1:], v) &&
					!escapeMentions(escapeArgs(in.ArgList), v):
					continue
				case in.Opcode == SlotSet && len(in.Operands) == 2 && viaSlot[v] != "" &&
					strings.TrimSpace(in.Operands[0]) == viaSlot[v] && strings.TrimSpace(in.Operands[1]) == v:
					// The demotion itself: the slot is the subject's, read only
					// where the loop ends.
					continue
				case isIterReset(in) && !escapeMentions(in.Operands, v):
					if args := escapeArgs(in.ArgList); len(args) > 0 && escapeToken(args[0], v) &&
						!escapeMentions(args[1:], v) {
						resets[v]++
						continue
					}
				}
				if escapeMentions(in.Operands, v) ||
					escapeMentions(escapeArgs(in.ArgList), v) ||
					(in.Result != nil && in.Result.String() == v) {
					delete(candidates, v)
				}
			}
		}
		for v := range candidates {
			if escapeToken(b.Term, v) {
				delete(candidates, v)
			}
		}
	}
	out := map[string]bool{}
	for v, class := range candidates {
		if resets[v] != 1 || ctorLeaks(class) || !plain(class) {
			continue
		}
		out[v] = true
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// loopSubject is the value an `iter.free.owned` names as its loop's subject.
func loopSubject(in Inst) (string, bool) {
	name := in.Opcode
	if in.Opcode == IntrinsicCall && len(in.Operands) > 0 {
		name = strings.Trim(in.Operands[0], `"`)
	}
	if name != "iter.free.owned" {
		return "", false
	}
	for _, a := range in.Attrs {
		if strings.HasPrefix(a, LoopSubjectAttr) {
			return strings.TrimPrefix(a, LoopSubjectAttr), true
		}
	}
	return "", false
}

// ThrowRegionAttr is the attribute an `iter.free` carries when it frees a loop
// on the way out through `return`, `break` or `continue` and an exception
// raised there belongs to a different region: the engine counts it as thrown
// at the loop's final free (ZEND_FREE_ON_RETURN), whose region may differ.
const ThrowRegionAttr = "throwregion="

// ThrowRegion is the region an `iter.free`'s exception belongs to, when the
// lowering recorded one.
func ThrowRegion(in Inst) (int, bool) {
	for _, a := range in.Attrs {
		if strings.HasPrefix(a, ThrowRegionAttr) {
			n, err := strconv.Atoi(strings.TrimPrefix(a, ThrowRegionAttr))
			return n, err == nil
		}
	}
	return 0, false
}

// LoopSubject is loopSubject for the emitter.
func LoopSubject(in Inst) (string, bool) { return loopSubject(in) }

// isIterReset reports a foreach's `iter.new` or `iter.new.ref`.
func isIterReset(in Inst) bool {
	if in.Opcode != IntrinsicCall || len(in.Operands) == 0 {
		return false
	}
	name := strings.Trim(in.Operands[0], `"`)
	return name == "iter.new" || name == "iter.new.ref"
}

// DiscardedResultsOf answers the results of calls and `clone`s that nothing
// reads and that hold a fresh value, which the engine frees at the end of the
// statement:
//
//	make("x");          // make() returns `new D(...)`
//	clone new D("y");   // the copy, after the operand
//
// A result is claimed only when no instruction or terminator mentions it --
// a `release_prev` marker included, since one names a value a promoted local
// held -- it is not the promoted form of a local (FrameBound: `$x = make();`
// with `$x` never read leaves the call's result looking unread), and
// `freshReturn` vouches for the callee as it does for a disposable
// temporary. A `clone` is fresh unless `__clone` may keep `$this`
// (leaksThis). Which value it turns out to be is the emitter's question: only
// an object is destroyed.
func DiscardedResultsOf(fn *Function, freshReturn func(callees []string) bool, leaksThis func(method string) bool) map[string]bool {
	if fn == nil || freshReturn == nil {
		return nil
	}
	mentioned := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			for _, tok := range append(append([]string{}, in.Operands...), escapeArgs(in.ArgList)...) {
				for _, v := range valueTokens(tok) {
					if in.Result == nil || in.Result.String() != v {
						mentioned[v] = true
					}
				}
			}
		}
		for _, v := range valueTokens(b.Term) {
			mentioned[v] = true
		}
		for _, p := range b.Params {
			name, _, _ := strings.Cut(p, ":")
			mentioned[strings.TrimSpace(name)] = true
		}
	}
	out := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Result == nil {
				continue
			}
			v := in.Result.String()
			if mentioned[v] || fn.FrameBound[v] {
				continue
			}
			fresh := false
			switch in.Opcode {
			case "call.fn", "call.static", "call.method":
				fresh = freshReturn(calleeIdentity(in))
			case "call.dynamic":
				// A closure literal, or a local that can only hold one
				// (ClosureCallees), names the body that answers.
				if len(in.Operands) == 0 {
					break
				}
				callee := strings.TrimSpace(in.Operands[0])
				target := fn.ClosureCallees[callee]
				if cdef := definition(fn, callee); cdef != nil && cdef.Opcode == "closure" && len(cdef.Operands) == 1 {
					target = strings.TrimSpace(cdef.Operands[0])
				}
				fresh = target != "" && freshReturn([]string{target})
			case "clone":
				fresh = leaksThis != nil && !leaksThis("__clone")
			}
			if fresh {
				out[v] = true
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
