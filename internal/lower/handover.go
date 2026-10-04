package lower

import "strings"

// TracesDieWithFrames reports a program in which a trace made while a frame
// is on the stack cannot hold that frame's arguments past the frame's
// normal return -- except through a catch inside the frame itself, which
// HoldsCaught answers per body.
//
// The engine keeps a call's arguments in the `args` of every trace made
// while the call is on the stack, so a parameter is not the only holder of
// its argument while such an exception lives. The ways one outlives the
// frames below the owner, each refused here:
//
//   - **A caught exception used as a value.** A catch variable lowers to
//     the handler's `$__excN` itself. Parked into it, type-tested, released
//     by a variable-less catch, or asked a *final* scalar getter of
//     Exception/Error (getMessage, getCode, getFile, getLine,
//     getTraceAsString) it keeps nothing and dies with the catching frame,
//     which is below the owner and returns first. Any other use -- stored,
//     returned, passed, asked for its trace or previous -- may keep it.
//   - **A throwable made and not thrown** (`$e = new Exception;`): its trace
//     is taken when it is made. Every `new` of a class isThrowable says may
//     be one must be used only by its constructor and a `throw`; a `new`
//     whose class is not written out (`new $c`, `new static`) could be any
//     class, so it refuses too.
//   - **`debug_backtrace()`**, which hands any callee the caller's arguments.
//
// An exception that leaves the owner is not covered and need not be: the
// owner did not return normally, and a taker leaves its arguments alone on
// that path.
//
// The file body (`{main}`) is exempt, because it is never below an owner:
// an exception its catch holds left every owner it passed by a throw, and a
// trace made there -- by a throwable or `debug_backtrace()` -- has no frame
// but its own, which has no arguments. So main may be nil or among bodies.
func TracesDieWithFrames(bodies []*Function, main *Function, isThrowable func(class string) bool) bool {
	for _, fn := range bodies {
		if fn == nil || fn == main {
			continue
		}
		if strings.Contains(strings.ToLower(fn.String()), `"debug_backtrace"`) {
			return false
		}
		for _, b := range fn.Blocks {
			if b.Dead {
				continue
			}
			if caughtToken(b.Term) != "" {
				return false
			}
			for _, in := range b.Insts {
				if !caughtUseKeepsNothing(in) {
					return false
				}
				if in.Opcode != "new" {
					continue
				}
				if in.Result == nil || len(in.Operands) == 0 {
					return false
				}
				class := strings.TrimSpace(in.Operands[0])
				if !strings.HasPrefix(class, `"`) {
					return false
				}
				if isThrowable(strings.ToLower(strings.Trim(class, `"`))) &&
					!onlyConstructedAndThrown(fn, in.Result.String()) {
					return false
				}
			}
		}
	}
	return true
}

// HoldsCaught reports a body that holds a caught exception as a variable of
// its own. The engine frees a frame's variables in order, so an exception
// the body itself caught is freed after its parameters -- and an argument
// that exception's trace holds dies there, not in the parameter's turn. A
// variable-less catch releases the exception at the catch and holds
// nothing.
func HoldsCaught(fn *Function) bool {
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		if caughtToken(b.Term) != "" {
			return true
		}
		for _, in := range b.Insts {
			if in.Opcode == ExcRelease {
				continue
			}
			if caughtToken(in.String()) != "" {
				return true
			}
		}
	}
	return false
}

// caughtFinalGetters are the Throwable methods the engine declares final
// and that answer a scalar: asking one keeps nothing of the exception.
var caughtFinalGetters = map[string]bool{
	"getmessage": true, "getcode": true, "getfile": true, "getline": true,
	"gettraceasstring": true,
}

// caughtUseKeepsNothing reports an instruction that either does not mention
// a caught exception or uses it in one of the ways that keep nothing.
func caughtUseKeepsNothing(in Inst) bool {
	exc := caughtToken(in.String())
	if exc == "" {
		return true
	}
	args := escapeArgs(in.ArgList)
	switch {
	case in.Opcode == ExcRelease:
		return true
	case in.Opcode == "instanceof" && len(in.Operands) > 0 && strings.TrimSpace(in.Operands[0]) == exc &&
		!escapeMentions(in.Operands[1:], exc):
		return true
	case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
		strings.Trim(in.Operands[0], `"`) == "write_var" && len(args) == 2 &&
		strings.TrimSpace(args[0]) == exc && !escapeToken(args[1], exc):
		// Parked from the handler's argument.
		return true
	case in.Opcode == "call.method" && len(in.Operands) >= 2 && strings.TrimSpace(in.Operands[0]) == exc &&
		caughtFinalGetters[strings.ToLower(strings.Trim(in.Operands[1], `"`))] &&
		!escapeMentions(in.Operands[2:], exc) && !escapeMentions(args, exc):
		return true
	}
	return false
}

// caughtToken finds a caught-exception handler value (`$__excN`, not the
// `$__excsave` a finally parks an unwinding exception in) mentioned in text.
func caughtToken(text string) string {
	for i := 0; ; {
		j := strings.Index(text[i:], "$__exc")
		if j < 0 {
			return ""
		}
		start := i + j
		end := start + len("$__exc")
		for end < len(text) && (text[end] >= '0' && text[end] <= '9' || text[end] >= 'a' && text[end] <= 'z' || text[end] == '_') {
			end++
		}
		tok := text[start:end]
		if !strings.HasPrefix(tok, "$__excsave") {
			return tok
		}
		i = end
	}
}

// HandOverAttr marks a method, static or closure call with the argument
// positions every body it may run owns (Function.OwnedParams), set by the
// compile path: `handover=0 2`.
const HandOverAttr = "handover"

// HandOverPositions reads a call's HandOverAttr.
func HandOverPositions(in Inst) map[int]bool {
	for _, a := range in.Attrs {
		rest, ok := strings.CutPrefix(a, HandOverAttr+"=")
		if !ok {
			continue
		}
		out := map[int]bool{}
		for _, f := range strings.Fields(rest) {
			n := 0
			for _, r := range f {
				n = n*10 + int(r-'0')
			}
			out[n] = true
		}
		return out
	}
	return nil
}

// Definition is the instruction defining value in fn, or nil.
func Definition(fn *Function, value string) *Inst { return definition(fn, value) }

// ArgKinds answers the send kind of each argument of a call's list, in
// order: `byval`, `preferref`, `byref`, or whatever else the list spells.
func ArgKinds(list string) []string {
	var out []string
	for _, a := range escapeArgs(list) {
		kind, _, _ := strings.Cut(strings.TrimSpace(a), " ")
		out = append(out, kind)
	}
	return out
}

// HandedOverArgs answers, for each method, static or closure call marked
// with HandOverAttr, the argument values at its marked positions that are
// fresh objects nothing but that call can hold: a `new` of a class written
// out whose constructor keeps nothing of `$this` (ctorLeaks), used by its
// own constructor call and as that one argument alone, not bound to a
// frame slot. Keyed by the call's position in fn (InstPos.Key, by block
// ID, as the emitter counts).
func HandedOverArgs(fn *Function, ctorLeaks func(class string) bool) map[string][]string {
	if fn == nil {
		return nil
	}
	var out map[string][]string
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for ii, in := range b.Insts {
			positions := HandOverPositions(in)
			if len(positions) == 0 {
				continue
			}
			args := escapeArgs(in.ArgList)
			for pos := range positions {
				if pos >= len(args) {
					continue
				}
				_, value, _ := strings.Cut(strings.TrimSpace(args[pos]), " ")
				value = strings.TrimSpace(value)
				if !strings.HasPrefix(value, "%") || fn.FrameBound[value] {
					continue
				}
				def := definition(fn, value)
				if def == nil || def.Opcode != "new" || len(def.Operands) == 0 {
					continue
				}
				class := strings.TrimSpace(def.Operands[0])
				if !strings.HasPrefix(class, `"`) || ctorLeaks(strings.ToLower(strings.Trim(class, `"`))) {
					continue
				}
				if !onlyConstructedAndStored(fn, value, in, pos) {
					continue
				}
				if escapeMentions(in.Operands, value) {
					continue
				}
				if out == nil {
					out = map[string][]string{}
				}
				key := InstPos{Block: b.ID, Index: ii}.Key()
				out[key] = append(out[key], value)
			}
		}
	}
	return out
}
