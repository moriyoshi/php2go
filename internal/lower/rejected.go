package lower

import "strings"

// Objects that die because a store refused them.
//
//	try { $t->typed = new stdClass; } catch (TypeError $e) {}
//	var_dump(new stdClass);   // #N, the refused object's number again
//
// The engine frees an operand when the opline that consumed it is done, and
// a store that throws is done too: the property never took the object, the
// temporary was its only holder, and the TypeError is already made when the
// object goes (so the error has the lower number, and the object's number is
// the next one handed out). Compiled code kept the refused object to the end
// of the script, and every later object came out one number high.
//
// This is the static half: which `new` results a store is their **only**
// consumer. The run-time half is in the emitter's disposeRejected, and it
// matters as much -- a store that refuses may already have handed the object
// to program code (`__set`, a set hook, `__toString` for a string property),
// and that code may have kept it. So the object is freed only when no frame
// was pushed during the store: then nothing but the engine ever held it.

// rejectingStores are the intrinsics that may refuse the value they are
// handed, with the argument position the value occupies.
var rejectingStores = map[string]int{
	"prop.set":      2,
	"sprop.set":     2,
	"coerce_return": 0,
}

// RejectableObjects answers the `new` results whose only uses are their own
// constructor call and one refusing store.
func RejectableObjects(fn *Function) map[string]bool {
	if fn == nil {
		return nil
	}
	out := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode != "call.intrinsic" || len(in.Operands) == 0 {
				continue
			}
			pos, ok := rejectingStores[strings.Trim(in.Operands[0], `"`)]
			if !ok {
				continue
			}
			args := escapeArgs(in.ArgList)
			if pos >= len(args) {
				continue
			}
			value := strings.TrimSpace(args[pos])
			if !strings.HasPrefix(value, "%") || fn.FrameBound[value] {
				continue
			}
			if def := definition(fn, value); def == nil || def.Opcode != "new" {
				continue
			}
			if onlyConstructedAndStored(fn, value, in, pos) {
				out[value] = true
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// onlyConstructedAndStored reports a value mentioned by nothing but its own
// definition, its constructor call as the receiver, and the one store.
func onlyConstructedAndStored(fn *Function, value string, store Inst, pos int) bool {
	stores := 0
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		if escapeToken(b.Term, value) {
			return false
		}
		for _, in := range b.Insts {
			if in.Result != nil && in.Result.String() == value {
				continue
			}
			args := escapeArgs(in.ArgList)
			if !escapeMentions(in.Operands, value) && !escapeMentions(args, value) {
				continue
			}
			if in.Opcode == "call.ctor" && len(in.Operands) > 0 &&
				strings.TrimSpace(in.Operands[0]) == value &&
				!escapeMentions(in.Operands[1:], value) && !escapeMentions(args, value) {
				continue
			}
			if in.Opcode == store.Opcode && len(in.Operands) > 0 && in.Operands[0] == store.Operands[0] &&
				in.ArgList == store.ArgList && pos < len(args) &&
				!escapeMentions(append(append([]string{}, args[:pos]...), args[pos+1:]...), value) &&
				!escapeMentions(in.Operands[1:], value) {
				stores++
				continue
			}
			return false
		}
	}
	return stores == 1
}
