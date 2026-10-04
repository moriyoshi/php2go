package lower

import "strings"

// FrameDrainOpcode is the instruction that destroys a resumable body's
// confined objects as it returns, put before each return when the body makes
// an object whose destructor may suspend the fiber (InsertFrameDrains). The
// CPS rewrite always splits at it, so the drain becomes a suspending call --
// `frame.of.frame_drain` and `call.suspending` -- and a destructor that
// suspends parks the stack instead of having nowhere to go
// (runtime/php framedrain.go).
const FrameDrainOpcode = "call.frame_drain"

// InsertFrameDrains puts a FrameDrainOpcode at the end of every live block
// that returns, and answers how many it put.
func InsertFrameDrains(fn *Function) int {
	n := 0
	for _, b := range fn.Blocks {
		if b == nil || b.Dead {
			continue
		}
		if strings.HasPrefix(b.Term, "ret ") || strings.HasPrefix(b.Term, "ret.ref ") {
			b.Insts = append(b.Insts, Inst{Opcode: FrameDrainOpcode})
			n++
		}
	}
	return n
}

// NewsClass answers the classes the body makes with a literal `new`, folded
// and without a leading backslash.
func NewsClass(fn *Function) []string {
	var out []string
	for _, b := range fn.Blocks {
		if b == nil || b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode != "new" || len(in.Operands) == 0 {
				continue
			}
			class := strings.TrimSpace(in.Operands[0])
			if !strings.HasPrefix(class, `"`) {
				continue
			}
			class = strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(strings.Trim(class, `"`), `\\`, `\`), `\`))
			out = append(out, class)
		}
	}
	return out
}

// InsertSiteDrains destroys a statement-level `new` -- one whose value only
// its own constructor ever sees, `new C(...);` -- right after that
// constructor, as the engine frees a statement's temporary at the statement's
// end, by putting a FrameDrainOpcode naming it after its `call.ctor`. Only for
// classes whose destructor may suspend: the drain is a suspension point. The
// emitter destroys the object there only if the confinement claim holds for
// it (the constructor keeps no `$this`); otherwise the drain does nothing.
// Answers the values it put one after.
func InsertSiteDrains(fn *Function, maySuspend func(class string) bool) []string {
	candidates := map[string]bool{}
	for _, b := range fn.Blocks {
		if b == nil || b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode != "new" || in.Result == nil || len(in.Operands) == 0 {
				continue
			}
			class := strings.TrimSpace(in.Operands[0])
			if !strings.HasPrefix(class, `"`) {
				continue
			}
			class = strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(strings.Trim(class, `"`), `\\`, `\`), `\`))
			if maySuspend(class) {
				candidates[in.Result.String()] = true
			}
		}
	}
	for _, b := range fn.Blocks {
		if b == nil || b.Dead {
			continue
		}
		for _, in := range b.Insts {
			for v := range candidates {
				if in.Opcode == "new" && in.Result != nil && in.Result.String() == v {
					continue
				}
				if in.Opcode == "call.ctor" && len(in.Operands) > 0 && escapeToken(in.Operands[0], v) &&
					!escapeMentions(escapeArgs(in.ArgList), v) {
					continue
				}
				if escapeMentions(in.Operands, v) || escapeMentions(escapeArgs(in.ArgList), v) ||
					(in.Result != nil && in.Result.String() == v) {
					delete(candidates, v)
				}
			}
		}
		for _, p := range b.Params {
			name, _, _ := strings.Cut(p, ":")
			delete(candidates, strings.TrimSpace(name))
		}
		for v := range candidates {
			if strings.Contains(b.Term, v) {
				delete(candidates, v)
			}
		}
	}
	var drained []string
	for _, b := range fn.Blocks {
		if b == nil || b.Dead {
			continue
		}
		for j := 0; j < len(b.Insts); j++ {
			in := b.Insts[j]
			if in.Opcode != "call.ctor" || len(in.Operands) == 0 {
				continue
			}
			v := strings.TrimSpace(in.Operands[0])
			if !candidates[v] {
				continue
			}
			drain := Inst{Opcode: FrameDrainOpcode, Operands: []string{v}}
			b.Insts = append(b.Insts[:j+1], append([]Inst{drain}, b.Insts[j+1:]...)...)
			delete(candidates, v)
			drained = append(drained, v)
			j++
		}
	}
	return drained
}

// DropCtorLeaking takes back the ConfinedNew claim of every `new` whose
// class's constructor may keep `$this` (ctorLeaks, keyed by the folded class
// name without a leading backslash). A `new` that names no literal class --
// `new static`, `new $name` -- is taken back when any constructor leaks
// (anyLeaks), since it may make any class.
func DropCtorLeaking(fn *Function, ctorLeaks func(class string) bool, anyLeaks bool) {
	if len(fn.ConfinedNew) == 0 {
		return
	}
	for _, b := range fn.Blocks {
		if b == nil || b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode != "new" || in.Result == nil || !fn.ConfinedNew[in.Result.String()] {
				continue
			}
			class := ""
			if len(in.Operands) > 0 && strings.HasPrefix(strings.TrimSpace(in.Operands[0]), `"`) {
				class = strings.ToLower(strings.TrimPrefix(strings.ReplaceAll(strings.Trim(strings.TrimSpace(in.Operands[0]), `"`), `\\`, `\`), `\`))
			}
			if class == "" && anyLeaks || class != "" && ctorLeaks(class) {
				delete(fn.ConfinedNew, in.Result.String())
			}
		}
	}
}
