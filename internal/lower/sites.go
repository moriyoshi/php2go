package lower

import (
	"fmt"
	"strings"
)

// Ownership proved per write rather than per variable.
//
// OverwriteOwners, RebindOwners and ReassignedLocals each prove a claim about
// a local for its whole life: every write fresh, every mention recognised. A
// local reused for unrelated things -- `$foo` holding an array in one
// statement, an ArrayAccess object in the next and a stdClass later
// (temporary_cleaning_013) -- fails all three, though each statement on its
// own is as clear as the single-use case.
//
// The per-write form asks about one interval instead: from a write of a fresh
// value to the next store into the same local **in the same block**, with no
// mention of the local in between. A block is entered only at its top, so
// every execution that reaches the second store has just made the first, and
// the value the second replaces is exactly the fresh one. What can still
// share it is the slot itself: a reference bound to the local (`=&`, a
// by-reference send, a `global` or `static` binding, a closure's `use (&$x)`)
// writes and reads it under another name. So no such binding may reach the
// interval -- none earlier in the block, and none in a block that can reach
// this one, exception edges included.

// SiteRelease is a store whose replaced value dies right after it, keyed in
// SiteReleases by the store's position ("block.index").
type SiteRelease struct {
	// Kind is "var" for a write of the local, "dim" / "prop" for an element or
	// property store into the container it holds, "rebind" for `=&`, and
	// "contents" for a write of the local that drops an owned container: an
	// array's elements, or a stdClass and its properties, die in order.
	Kind string
	// News are the `new` results the interval proves the local or its
	// container may hold: the emitter asks, per class, whether a constructor
	// keeps one.
	News []string
}

// slotTaint answers whether a local's slot may be bound as a reference when
// control reaches a block.
type slotTaint struct {
	fn *Function
	// reach is, per local, the blocks (by index) a binding of it can reach.
	reach map[string][]bool
}

// localTokens are the `$name` tokens of a rendered operand or argument.
func localTokens(text string) []string {
	var out []string
	for i := 0; i < len(text); i++ {
		if text[i] != '$' {
			continue
		}
		j := i + 1
		for j < len(text) && (text[j] == '_' || text[j] >= 'a' && text[j] <= 'z' ||
			text[j] >= 'A' && text[j] <= 'Z' || text[j] >= '0' && text[j] <= '9' || text[j] >= 0x80) {
			j++
		}
		if j > i+1 {
			out = append(out, text[i:j])
		}
		i = j - 1
	}
	return out
}

// mentionedLocals are the locals an instruction names.
func mentionedLocals(in Inst) map[string]bool {
	out := map[string]bool{}
	for _, tok := range append(append([]string{}, in.Operands...), escapeArgs(in.ArgList)...) {
		for _, l := range localTokens(tok) {
			out[l] = true
		}
	}
	return out
}

// bindsSlot reports a mention of the local that may bind its slot to other
// storage or hand the slot out: anything but a plain write, a read, an unset,
// an element or property operation through it, or a by-value send.
func bindsSlot(in Inst, local string) bool {
	if target, value, ok := localWrite(in); ok && target == local && !escapeToken(value, local) {
		return false
	}
	args := escapeArgs(in.ArgList)
	switch {
	case in.Opcode == ReleasePrev, in.Opcode == "local.get":
		return false
	case in.Opcode == "unset" && len(in.Operands) == 1 && strings.TrimSpace(in.Operands[0]) == local:
		return false
	case (in.Opcode == "isset" || in.Opcode == "empty") && len(in.Operands) >= 1 &&
		strings.TrimSpace(in.Operands[0]) == local && !escapeMentions(in.Operands[1:], local):
		return false
	case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 && len(args) >= 1 &&
		strings.TrimSpace(args[0]) == local && !escapeMentions(args[1:], local) &&
		!escapeMentions(in.Operands[1:], local):
		op := strings.Trim(in.Operands[0], `"`)
		if op == "read_var" || op == "read_var.silent" || escapeIsRead(op) ||
			strings.HasPrefix(op, "dim.") || strings.HasPrefix(op, "prop.") {
			return false
		}
		return true
	case in.Opcode == "call.fn" && !escapeMentions(in.Operands, local):
		for _, a := range args {
			if escapeToken(a, local) && !strings.HasPrefix(strings.TrimSpace(a), "byval ") {
				return true
			}
		}
		return false
	}
	return true
}

func newSlotTaint(fn *Function) *slotTaint {
	index := map[int]int{}
	for i, b := range fn.Blocks {
		index[b.ID] = i
	}
	succs := make([][]int, len(fn.Blocks))
	for i, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		succs[i] = append(succs[i], b.Succs...)
		// **Exception edges**: an instruction in a region may leave for its
		// handlers, and an unhandled exception goes on outward.
		for r := b.Region; r >= 0 && r < len(fn.Regions); r = fn.Regions[r].Parent {
			for _, h := range []int{fn.Regions[r].Catch, fn.Regions[r].Finally} {
				if at, ok := index[h]; ok && h >= 0 {
					succs[i] = append(succs[i], at)
				}
			}
		}
	}
	t := &slotTaint{fn: fn, reach: map[string][]bool{}}
	for i, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			for local := range mentionedLocals(in) {
				if !bindsSlot(in, local) {
					continue
				}
				seen := t.reach[local]
				if seen == nil {
					seen = make([]bool, len(fn.Blocks))
					t.reach[local] = seen
				}
				stack := append([]int{}, succs[i]...)
				for len(stack) > 0 {
					n := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					if n < 0 || n >= len(seen) || seen[n] {
						continue
					}
					seen[n] = true
					stack = append(stack, succs[n]...)
				}
			}
		}
	}
	return t
}

// boundBefore reports that the local's slot may be bound when control is at
// instruction `at` of block `bi`: a binding earlier in the block, or one in a
// block that can reach this one.
func (t *slotTaint) boundBefore(local string, bi, at int) bool {
	if seen := t.reach[local]; seen != nil && seen[bi] {
		return true
	}
	for _, in := range t.fn.Blocks[bi].Insts[:at] {
		if mentionedLocals(in)[local] && bindsSlot(in, local) {
			return true
		}
	}
	return false
}

// SiteReleases answers the stores whose replaced value is proved owned by the
// interval since the local's last write in the same block.
//
// `reachedByGlobal` must answer true for a name another body can reach --
// `global $x`, or any `$GLOBALS` at all.
func SiteReleases(fn *Function, reachedByGlobal func(name string) bool,
	keepsArg func(callees []string, param int) bool,
	leaksThis func(method string) bool, argKept func(method string) bool) map[string]SiteRelease {
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
	freshNew := func(v string) bool {
		v = strings.TrimSpace(v)
		def := definition(fn, v)
		return def != nil && def.Opcode == "new" && uses[v] == 2 && usedOnlyBy(fn, v, "call.ctor")
	}
	// literal is an array literal built here from fresh objects and values
	// holding none, used by nothing but its write: it answers the objects.
	var literal func(v string, stringKeys bool) ([]string, bool)
	literal = func(v string, stringKeys bool) ([]string, bool) {
		v = strings.TrimSpace(v)
		if !strings.HasPrefix(v, "%") {
			return nil, strings.HasPrefix(v, "[") && literalArray(v)
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
			if len(args) != 3 || strings.HasPrefix(strings.TrimSpace(args[1]), "%") ||
				stringKeys && !strings.HasPrefix(strings.TrimSpace(args[1]), `"`) {
				return nil, false
			}
		default:
			return nil, false
		}
		news, ok := literal(args[0], stringKeys)
		if !ok {
			return nil, false
		}
		switch el := strings.TrimSpace(args[len(args)-1]); {
		case freshNew(el):
			news = append(news, el)
		case scalar(el):
		default:
			return nil, false
		}
		return news, true
	}
	// state is what an interval proves the local holds.
	type state struct {
		kind string // "var", "dim", "prop"
		news []string
	}
	written := func(v string) (state, bool) {
		v = strings.TrimSpace(v)
		switch {
		case freshNew(v):
			if def := definition(fn, v); def != nil && len(def.Operands) == 1 &&
				strings.EqualFold(strings.Trim(def.Operands[0], `"\`), "stdClass") {
				return state{kind: "prop"}, true
			}
			return state{kind: "var", news: []string{v}}, true
		case nonObjectLiteral(v):
			return state{kind: "var"}, true
		}
		if news, ok := literal(v, false); ok {
			return state{kind: "dim", news: news}, true
		}
		if def := definition(fn, v); def != nil && def.Opcode == "call.intrinsic" && len(def.Operands) > 0 &&
			strings.Trim(def.Operands[0], `"`) == "to.object" && uses[v] == 1 {
			if a := escapeArgs(def.ArgList); len(a) == 1 {
				if news, ok := literal(a[0], true); ok {
					return state{kind: "prop", news: news}, true
				}
			}
		}
		return state{}, false
	}
	stored := func(v string) (string, bool) {
		v = strings.TrimSpace(v)
		if freshNew(v) {
			return v, true
		}
		return "", scalar(v)
	}
	resultSafe := func(in Inst) bool {
		return in.Result == nil || uses[in.Result.String()] == 0 || ea.derived(in.Result.String())
	}
	var taint *slotTaint
	out := map[string]SiteRelease{}
	for bi, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		held := map[string]state{}
		// pending maps a `prop.set` position to the local whose `read_var`
		// feeds it, while nothing else has touched the local since.
		pending := map[int]string{}
		for i, in := range b.Insts {
			fresh := map[int]bool{}
			pos := fmt.Sprintf("%d.%d", b.ID, i)
			mentioned := mentionedLocals(in)
			if local, ok := pending[i]; ok {
				delete(pending, i)
				args := escapeArgs(in.ArgList)
				if st, live := held[local]; live && st.kind == "prop" && len(args) == 3 && resultSafe(in) &&
					strings.HasPrefix(strings.TrimSpace(args[1]), `"`) {
					if n, ok := stored(args[2]); ok {
						out[pos] = SiteRelease{Kind: "prop", News: append([]string{}, st.news...)}
						if n != "" {
							st.news = append(st.news, n)
						}
						held[local] = st
						continue
					}
				}
				delete(held, local)
			}
			for local := range mentioned {
				if strings.HasPrefix(local, "$__") || reachedByGlobal(strings.TrimPrefix(local, "$")) {
					delete(held, local)
					continue
				}
				st, live := held[local]
				bound := func() bool {
					if taint == nil {
						taint = newSlotTaint(fn)
					}
					return taint.boundBefore(local, bi, i)
				}
				switch target, value, isWrite := localWrite(in); {
				case isWrite && target == local && !escapeToken(value, local):
					if live && st.kind == "var" && len(st.news) > 0 && !bound() {
						out[pos] = SiteRelease{Kind: "var", News: st.news}
					}
					if live && (st.kind == "dim" || st.kind == "prop") && !bound() {
						out[pos] = SiteRelease{Kind: "contents", News: st.news}
					}
					if next, ok := written(value); ok && !bound() {
						held[local] = next
					} else {
						delete(held, local)
					}
				case in.Opcode == "local.get" && ReadsBackWrite(b, in):
					if in.Result != nil && uses[in.Result.String()] > 0 && !ea.derived(in.Result.String()) {
						delete(held, local)
					}
				case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
					strings.Trim(in.Operands[0], `"`) == "dim.set" && live && st.kind == "dim":
					args := escapeArgs(in.ArgList)
					n, ok := "", false
					if len(args) == 3 && strings.TrimSpace(args[0]) == local && !escapeMentions(args[1:], local) &&
						!strings.HasPrefix(strings.TrimSpace(args[1]), "%") && resultSafe(in) {
						n, ok = stored(args[2])
					}
					if !ok {
						delete(held, local)
						break
					}
					out[pos] = SiteRelease{Kind: "dim", News: append([]string{}, st.news...)}
					if n != "" {
						st.news = append(st.news, n)
					}
					held[local] = st
				case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 && live && st.kind == "prop" &&
					(strings.Trim(in.Operands[0], `"`) == "read_var" || strings.Trim(in.Operands[0], `"`) == "read_var.silent") &&
					in.Result != nil && uses[in.Result.String()] == 1:
					// The container read for a property store: the store is
					// its one use, later in the block.
					r := in.Result.String()
					found := -1
					for j := i + 1; j < len(b.Insts); j++ {
						a := escapeArgs(b.Insts[j].ArgList)
						if escapeMentions(a, r) || escapeMentions(b.Insts[j].Operands, r) {
							if b.Insts[j].Opcode == "call.intrinsic" && len(b.Insts[j].Operands) > 0 &&
								strings.Trim(b.Insts[j].Operands[0], `"`) == "prop.set" && len(a) == 3 &&
								strings.TrimSpace(a[0]) == r && !escapeMentions(a[1:], r) {
								found = j
							}
							break
						}
					}
					if found < 0 || strings.Contains(b.Term, r) {
						delete(held, local)
						break
					}
					pending[found] = local
					fresh[found] = true
				case in.Opcode == "bind_ref" && len(in.Operands) == 2 && strings.TrimSpace(in.Operands[0]) == local &&
					strings.TrimSpace(in.Operands[1]) != local:
					if live && st.kind == "var" && len(st.news) > 0 && !bound() {
						out[pos] = SiteRelease{Kind: "rebind", News: st.news}
					}
					delete(held, local)
				case in.Opcode == ReleasePrev:
				default:
					delete(held, local)
				}
			}
			// A pending store whose local was touched meanwhile is dropped.
			for at, local := range pending {
				if mentioned[local] && !fresh[at] {
					delete(pending, at)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ClosureCallees answers the `read_var` results that can only be one closure
// literal, keyed by the read's value, with the literal's target: the local
// was written with a `closure` used by nothing else, earlier in the same
// block, not mentioned since, and its slot is bound to nothing
// (`(function () {...})` held in `$f` for `$f()`, temporary_cleaning_013).
func ClosureCallees(fn *Function, reachedByGlobal func(name string) bool) map[string]string {
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
	var taint *slotTaint
	out := map[string]string{}
	for bi, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		held := map[string]string{}
		for i, in := range b.Insts {
			for local := range mentionedLocals(in) {
				target, known := held[local]
				switch w, value, isWrite := localWrite(in); {
				case isWrite && w == local:
					delete(held, local)
					value = strings.TrimSpace(value)
					def := definition(fn, value)
					if def == nil || def.Opcode != "closure" || len(def.Operands) != 1 || uses[value] != 1 ||
						reachedByGlobal(strings.TrimPrefix(local, "$")) {
						break
					}
					if taint == nil {
						taint = newSlotTaint(fn)
					}
					if !taint.boundBefore(local, bi, i) {
						held[local] = strings.TrimSpace(def.Operands[0])
					}
				case known && in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
					strings.Trim(in.Operands[0], `"`) == "read_var" && in.Result != nil:
					if args := escapeArgs(in.ArgList); len(args) == 1 && strings.TrimSpace(args[0]) == local {
						out[in.Result.String()] = target
					} else {
						delete(held, local)
					}
				default:
					delete(held, local)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
