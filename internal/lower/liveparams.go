package lower

import (
	"strconv"
	"strings"
)

// LiveParams finds the parameters a body **writes after binding them**, keyed
// by place and answering the argument position.
//
// The engine keeps a call's arguments in the callee's own CV slots, so what a
// trace, `debug_backtrace()` or `func_get_args()` reports for a parameter is
// what the parameter holds *now*, not what was passed: `$a = 5; g();` shows 5
// in g's backtrace, an `unset($a)` shows NULL, and an exception made after the
// write records the new value (bug70547, bug73156). The frame the caller
// pushed holds the values passed, so a parameter the body never writes is
// already right there; one it does write has to be read through its slot.
//
// Such a parameter keeps its slot: Mem2Reg will not promote it and
// placeRepresentation keeps it a zval, so the emitter can hand the frame its
// address (php.Ctx.BindLiveArgs).
//
// Conservative by construction: every mention of the place that is not a plain
// read, and not the binding the prologue makes from `recv`, counts as a write
// -- a by-reference or prefer-reference send included, since the callee may
// assign through it. A variadic parameter is not an argument slot (the
// arguments it collected stay in the frame as passed) and is never listed.
func LiveParams(fn *Function) map[string]int {
	params := map[string]int{}
	for _, p := range fn.Params {
		fields := strings.SplitN(strings.TrimSpace(p), " ", 3)
		if len(fields) < 2 {
			continue
		}
		pos, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		params[strings.TrimPrefix(fields[1], "&")] = pos
	}
	if len(params) == 0 {
		return nil
	}
	recvd := map[string]bool{}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Result == nil {
				continue
			}
			switch in.Opcode {
			case "recv", "recv.init":
				recvd[in.Result.String()] = true
			case "recv.variadic":
				if len(in.Operands) > 0 {
					if pos, err := strconv.Atoi(strings.TrimSpace(in.Operands[0])); err == nil {
						for name, at := range params {
							if at == pos {
								delete(params, name)
							}
						}
					}
				}
			}
		}
	}
	live := map[string]int{}
	// **A frame reached by name** -- a variable variable, an include or eval,
	// extract() -- may write any of its variables without naming one, so
	// every parameter is read through its slot (bug73156: `$args = array()`
	// in an eval'd string inside __call).
	if frameIsOpaque(fn) != "" {
		for name, pos := range params {
			live[name] = pos
		}
		return live
	}
	mark := func(op string) {
		op = strings.TrimSpace(op)
		for _, mode := range []string{"byref ", "preferref ", "byval "} {
			if strings.HasPrefix(op, mode) {
				if mode == "byval " {
					return
				}
				op = strings.TrimSpace(strings.TrimPrefix(op, mode))
			}
		}
		if pos, ok := params[op]; ok {
			live[op] = pos
		}
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Directive() {
				continue
			}
			if place, value, ok := StoreOf(in); ok {
				if recvd[strings.TrimSpace(value)] {
					continue
				}
				mark(place)
				continue
			}
			if _, v, ok := IntrinsicOf(in); ok {
				if strings.HasPrefix(v.Name, "read_var") {
					continue
				}
				for _, a := range CallArgs(in) {
					mark(a)
				}
				continue
			}
			for _, op := range in.Operands {
				mark(op)
			}
			for _, a := range CallArgs(in) {
				mark(a)
			}
		}
	}
	if len(live) == 0 {
		return nil
	}
	return live
}
