package lower

import "strings"

// Objects that die when the element or property holding them is overwritten.
//
//	$a = [new D];        $a[0] = new E;     // D dies here
//	$o = (object)["p" => new D]; $o->p = 1; // D dies here
//
// The engine destroys the replaced value right after the store -- its result
// already taken, so `var_dump($a[0] = new E)` prints E whatever D's destructor
// does (gh10168/assign_dim) -- and a throw from the destructor is thrown by
// the statement (temporary_cleaning_013). Which value is replaced is a
// run-time question; whether anything else holds it is not, when the
// container is a local that nothing but these writes ever touches.

// OverwriteOwner is a local whose container solely owns every object stored
// in it, and how the program writes into it.
type OverwriteOwner struct {
	// Kind is "dim" for an array written by `dim.set`, "prop" for a stdClass
	// written by `prop.set`.
	Kind string
	// News are the `new` results ever stored into the container: the emitter
	// asks, per class, whether a constructor or destructor may keep one.
	News []string
	// Receivers are the store receivers that write into this container: the
	// local itself for `dim.set`, the `read_var` result for `prop.set`.
	Receivers []string
}

// OverwriteOwners answers the stores whose replaced element or property may
// be destroyed right after them, keyed by the store's receiver as it appears
// in its argument list.
//
// Every mention of the local must be recognised:
//
//   - a write of a **fresh container** -- an array literal built here (or an
//     immediate one) for "dim"; `(object)` of such a literal, or `new
//     stdClass`, for "prop" -- whose elements are each a `new` used by its
//     constructor and the literal alone, or hold no object at any depth;
//   - `dim.set($l, K, V)`, or `prop.set` through a `read_var` of the local
//     used by nothing else, with a literal key or name and V a `new` used by
//     its constructor and this store alone, or a value holding no object; the
//     store's result, when used, goes only where the frame keeps nothing;
//   - `unset`, and the release markers.
//
// Nothing reads the container, copies it, iterates it, references into it or
// hands it anywhere, and no other body binds it as a global, so the only
// holder of what it stores is the container. A stdClass runs no code on a
// property write, and an array none on an element write.
func OverwriteOwners(fn *Function, reachedByGlobal func(name string) bool,
	keepsArg func(callees []string, param int) bool,
	leaksThis func(method string) bool, argKept func(method string) bool) map[string]OverwriteOwner {
	if fn == nil || reachedByGlobal == nil {
		return nil
	}
	text := fn.String()
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	if strings.Contains(text, "dynvar") {
		return nil
	}
	uses := useCounts(fn)
	ea := &escapes{fn: fn, keepsArg: keepsArg, leaksThis: leaksThis, argKept: argKept}
	scalar := func(v string) bool { return deepScalar(fn, v, reachedByGlobal, map[string]int{}) }
	// freshNew is a `new` used by its constructor and one store.
	freshNew := func(v string) bool {
		def := definition(fn, v)
		return def != nil && def.Opcode == "new" && uses[v] == 2 && usedOnlyBy(fn, v, "call.ctor")
	}
	// ownedLiteral is an array literal built here whose object elements are
	// each a freshNew; it answers those elements.
	var ownedLiteral func(v string, stringKeys bool) ([]string, bool)
	ownedLiteral = func(v string, stringKeys bool) ([]string, bool) {
		v = strings.TrimSpace(v)
		if !strings.HasPrefix(v, "%") {
			if strings.HasPrefix(v, "[") && literalArray(v) {
				return nil, true
			}
			return nil, false
		}
		def := definition(fn, v)
		if def == nil || def.Opcode != "call.intrinsic" || len(def.Operands) == 0 || uses[v] != 1 {
			return nil, false
		}
		args := ArgValues(def.ArgList)
		switch strings.Trim(def.Operands[0], `"`) {
		case "array.new":
			return nil, true
		case "array.append":
			if stringKeys || len(args) != 2 {
				return nil, false
			}
		case "array.set", "array.set.key":
			if len(args) != 3 || strings.HasPrefix(strings.TrimSpace(args[1]), "%") {
				return nil, false
			}
			if stringKeys && !strings.HasPrefix(strings.TrimSpace(args[1]), `"`) {
				return nil, false
			}
		default:
			return nil, false
		}
		news, ok := ownedLiteral(args[0], stringKeys)
		if !ok {
			return nil, false
		}
		el := strings.TrimSpace(args[len(args)-1])
		switch {
		case freshNew(el):
			news = append(news, el)
		case scalar(el):
		default:
			return nil, false
		}
		return news, true
	}
	// resultSafe is a store's result: unused, or used where nothing keeps it.
	resultSafe := func(in Inst) bool {
		return in.Result == nil || uses[in.Result.String()] == 0 || ea.derived(in.Result.String())
	}
	// storedValue checks a value written into the container.
	storedValue := func(v string) (string, bool) {
		v = strings.TrimSpace(v)
		if freshNew(v) {
			return v, true
		}
		return "", scalar(v)
	}
	locals := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if local, _, ok := localWrite(in); ok {
				locals[local] = true
			}
		}
	}
	out := map[string]OverwriteOwner{}
	for local := range locals {
		if reachedByGlobal(strings.TrimPrefix(local, "$")) {
			continue
		}
		kind, news, ok := "", []string(nil), true
		var receivers []string
		setKind := func(k string) {
			if kind != "" && kind != k {
				ok = false
			}
			kind = k
		}
		for _, b := range fn.Blocks {
			if b.Dead || !ok {
				continue
			}
			if escapeToken(b.Term, local) {
				ok = false
				break
			}
			for _, in := range b.Insts {
				args := escapeArgs(in.ArgList)
				if !escapeMentions(in.Operands, local) && !escapeMentions(args, local) {
					continue
				}
				if target, value, isWrite := localWrite(in); isWrite && target == local {
					if escapeToken(value, local) {
						ok = false
						break
					}
					if nonObjectLiteral(value) {
						continue
					}
					if n, lit := ownedLiteral(value, false); lit {
						setKind("dim")
						news = append(news, n...)
						continue
					}
					def := definition(fn, strings.TrimSpace(value))
					switch {
					case def != nil && def.Opcode == "new" && len(def.Operands) == 1 &&
						strings.EqualFold(strings.Trim(def.Operands[0], `"\`), "stdClass") &&
						uses[strings.TrimSpace(value)] == 2:
						// Its constructor and this write: nothing else has it.
						setKind("prop")
					case def != nil && def.Opcode == "call.intrinsic" && len(def.Operands) > 0 &&
						strings.Trim(def.Operands[0], `"`) == "to.object" && uses[strings.TrimSpace(value)] == 1:
						a := escapeArgs(def.ArgList)
						n, lit := []string(nil), false
						if len(a) == 1 {
							n, lit = ownedLiteral(a[0], true)
						}
						if !lit {
							ok = false
							break
						}
						setKind("prop")
						news = append(news, n...)
					default:
						ok = false
					}
					if !ok {
						break
					}
					continue
				}
				switch {
				case in.Opcode == "unset" && len(in.Operands) == 1 && strings.TrimSpace(in.Operands[0]) == local:
				case in.Opcode == ReleasePrev:
				case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
					strings.Trim(in.Operands[0], `"`) == "dim.set" && len(args) == 3 &&
					strings.TrimSpace(args[0]) == local && !escapeMentions(args[1:], local) &&
					!strings.HasPrefix(strings.TrimSpace(args[1]), "%") && resultSafe(in):
					n, stored := storedValue(args[2])
					if !stored {
						ok = false
						break
					}
					setKind("dim")
					receivers = append(receivers, local)
					if n != "" {
						news = append(news, n)
					}
				case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
					(strings.Trim(in.Operands[0], `"`) == "read_var" || strings.Trim(in.Operands[0], `"`) == "read_var.silent") &&
					len(args) == 1 && in.Result != nil && uses[in.Result.String()] == 1:
					n, stored := propStoreThrough(fn, in.Result.String(), storedValue, resultSafe)
					if !stored {
						ok = false
						break
					}
					setKind("prop")
					receivers = append(receivers, in.Result.String())
					if n != "" {
						news = append(news, n)
					}
				default:
					ok = false
				}
				if !ok {
					break
				}
			}
		}
		if ok && kind != "" && len(receivers) > 0 {
			owner := OverwriteOwner{Kind: kind, News: news, Receivers: receivers}
			for _, r := range receivers {
				out[r] = owner
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// propStoreThrough finds the one use of a container read: the receiver of a
// `prop.set` with a literal name. It answers the `new` stored, if any.
func propStoreThrough(fn *Function, recv string, stored func(string) (string, bool),
	resultSafe func(Inst) bool) (string, bool) {
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			args := escapeArgs(in.ArgList)
			if !escapeMentions(args, recv) && !escapeMentions(in.Operands, recv) {
				continue
			}
			if in.Opcode != "call.intrinsic" || len(in.Operands) == 0 ||
				strings.Trim(in.Operands[0], `"`) != "prop.set" || len(args) != 3 ||
				strings.TrimSpace(args[0]) != recv || escapeMentions(args[1:], recv) ||
				!strings.HasPrefix(strings.TrimSpace(args[1]), `"`) || !resultSafe(in) {
				return "", false
			}
			return stored(args[2])
		}
	}
	return "", false
}

// RebindOwners answers the locals whose object dies at the `$l = &$other`
// that rebinds them, with the `new` results the local ever held.
//
// The engine destroys what the variable held when the reference binding
// replaces it -- before the assignment's value is read back
// (gh10168/assign_ref: a destructor that clears the new referent makes the
// expression NULL). Claimed only when the local is this frame's sole holder
// of every object it held up to then, and nothing reaches it after:
//
//   - every mention of the local sits in a block control cannot come back to;
//   - they are writes of a `new` used by its constructor and that write alone
//     (or of a value holding no object), then exactly one `bind_ref $l, …`
//     naming it first, then at most the read-back of the assignment's value,
//     used where the frame keeps nothing -- a write in another block must not
//     be reachable from the rebind's, so every path sees them in that order;
//   - no other body binds it as a global.
//
// After the rebind the local is an alias of the other variable, so no later
// release may touch it -- which is why nothing may follow but the read-back.
func RebindOwners(fn *Function, reachedByGlobal func(name string) bool,
	keepsArg func(callees []string, param int) bool,
	leaksThis func(method string) bool, argKept func(method string) bool) map[string][]string {
	if fn == nil || reachedByGlobal == nil {
		return nil
	}
	text := fn.String()
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	if strings.Contains(text, "dynvar") {
		return nil
	}
	uses := useCounts(fn)
	ea := &escapes{fn: fn, keepsArg: keepsArg, leaksThis: leaksThis, argKept: argKept}
	cfg := Analyze(fn)
	inCycle := cfg.InCycle()
	out := map[string][]string{}
	for bi, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for i, in := range b.Insts {
			if in.Opcode != "bind_ref" || len(in.Operands) != 2 {
				continue
			}
			local := strings.TrimSpace(in.Operands[0])
			if !strings.HasPrefix(local, "$") || strings.HasPrefix(local, "$__") ||
				strings.TrimSpace(in.Operands[1]) == local || reachedByGlobal(strings.TrimPrefix(local, "$")) ||
				bi < len(inCycle) && inCycle[bi] {
				continue
			}
			news, ok := rebindMentions(fn, cfg, inCycle, bi, i, local, uses, ea, reachedByGlobal)
			if ok {
				out[local] = news
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func rebindMentions(fn *Function, cfg *CFG, inCycle []bool, bindAt, at int, local string,
	uses map[string]int, ea *escapes, reachedByGlobal func(string) bool) ([]string, bool) {
	// after is every block the rebind's block can reach.
	after := map[int]bool{}
	stack := append([]int{}, cfg.Succs(bindAt)...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if after[n] {
			continue
		}
		after[n] = true
		stack = append(stack, cfg.Succs(n)...)
	}
	var news []string
	for bi, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		if escapeToken(b.Term, local) {
			return nil, false
		}
		for i, in := range b.Insts {
			args := escapeArgs(in.ArgList)
			if !escapeMentions(in.Operands, local) && !escapeMentions(args, local) {
				continue
			}
			here := bi == bindAt
			if !here && (after[bi] || bi < len(inCycle) && inCycle[bi]) {
				return nil, false
			}
			switch {
			case here && i == at:
			case in.Opcode == ReleasePrev:
			case !here || i < at:
				target, value, ok := localWrite(in)
				if !ok || target != local || escapeToken(value, local) {
					return nil, false
				}
				value = strings.TrimSpace(value)
				if def := definition(fn, value); def != nil && def.Opcode == "new" && uses[value] == 2 &&
					usedOnlyBy(fn, value, "call.ctor") {
					news = append(news, value)
					continue
				}
				if !deepScalar(fn, value, reachedByGlobal, map[string]int{}) {
					return nil, false
				}
			case here && i == at+1 && in.Opcode == "local.get" && len(in.Operands) == 1 &&
				strings.TrimSpace(in.Operands[0]) == local && in.Result != nil:
				if uses[in.Result.String()] > 0 && !ea.derived(in.Result.String()) {
					return nil, false
				}
			default:
				return nil, false
			}
		}
	}
	return news, true
}
