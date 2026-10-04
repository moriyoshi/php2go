package lower

import (
	"sort"
	"strconv"
	"strings"
)

// In-place string appends: P1 of OWNERSHIP-PERF.md.
//
// `$s .= "x"` in a loop is quadratic against immutable Go strings -- every
// concatenation copies everything so far -- where the engine appends in place
// at refcount 1. bench.php's `strcat` spent 11 of its 18 seconds there. The
// builder-promotion pass that was meant to catch it looks for a *variable*,
// and by the time it runs promotion has turned `$s` into a loop-carried SSA
// value, so it found nothing.
//
// This rewrite works on the SSA values instead, and takes its proof from the
// ownership trace: a concatenation is an **append** when its left operand dies
// there, is this body's own, is held by no local, and has no other live name
// (`OwnershipTrace.Appends`). The values of such a chain are carried as the
// `*strbuf` representation the rope lowering already uses -- a Go
// `strings.Builder` -- and every other use of one reads a **snapshot**
// (`strbuf.finish`), which is O(1): a builder only ever writes past the bytes it
// has already handed out, so a snapshot never changes afterwards.
//
// What the rewrite must never do is let two live values share a buffer when one
// of them appends. Every value in a group below may be the same Go builder at
// run time, so at each append nothing else in the group may be live. The other
// thing it must not do is move a conversion: a chain may only be entered with a
// value already known to be a string -- a string literal, or a concatenation --
// so `$s = 0; ... $s .= "x"` never converts the `0` earlier than PHP would, or
// at all when the loop runs zero times.

// StringChains rewrites every loop-carried concatenation chain the trace proves
// safe into in-place appends, and reports how many concatenations it rewrote.
func StringChains(fn *Function) int {
	if len(fn.States) > 0 || fn.SuspendsToCaller || ownershipOpaque(fn) {
		return 0
	}
	tr := Trace(fn, OwnershipOptions{})
	if len(tr.Appends) == 0 {
		return 0
	}
	_, after := valueLiveness(fn)

	type def struct {
		param        bool
		block, index int
	}
	defs := map[string]def{}
	blocks := map[int]*Block{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		blocks[b.ID] = b
		for i, p := range b.Params {
			if t := ssaTokens(p); len(t) > 0 {
				defs[t[0]] = def{param: true, block: b.ID, index: i}
			}
		}
		for i, in := range b.Insts {
			if in.Result != nil {
				defs[in.Result.String()] = def{block: b.ID, index: i}
			}
		}
	}
	appendOf := map[string]Append{}
	leftOf := map[string][]Append{}
	for _, a := range tr.Appends {
		appendOf[a.Result] = a
		leftOf[a.Left] = append(leftOf[a.Left], a)
	}
	isConcat := func(tok string) bool {
		d, ok := defs[tok]
		if !ok || d.param {
			return false
		}
		in := blocks[d.block].Insts[d.index]
		return in.Opcode == IntrinsicCall && len(in.Operands) > 0 &&
			strings.Trim(in.Operands[0], `"`) == "concat" && len(CallArgs(in)) == 2
	}

	// Groups: the identities a buffer can flow through. An append joins its
	// left operand's identity to its result's, because at run time they are
	// the same builder.
	group := map[string]int{}
	var groups [][]string
	for _, a := range tr.Appends {
		if _, done := group[a.Left]; done {
			continue
		}
		id := len(groups)
		var members []string
		for queue := []string{a.Left, a.Result}; len(queue) > 0; {
			tok := queue[0]
			queue = queue[1:]
			for _, m := range tr.identityMembers(tok) {
				if _, seen := group[m]; seen {
					continue
				}
				group[m] = id
				members = append(members, m)
				if ap, ok := appendOf[m]; ok {
					queue = append(queue, ap.Left)
				}
				for _, ap := range leftOf[m] {
					queue = append(queue, ap.Result)
				}
			}
		}
		sort.Strings(members)
		groups = append(groups, members)
	}

	// Incoming edge arguments per block parameter, from every terminator.
	incoming := map[string][]string{}
	for _, b := range blocks {
		for _, t := range ownTargets(b.Term) {
			tb := blocks[t.block]
			if tb == nil {
				continue
			}
			for i, a := range t.args {
				if i < len(tb.Params) {
					if p := ssaTokens(tb.Params[i]); len(p) > 0 {
						incoming[p[0]] = append(incoming[p[0]], strings.TrimSpace(a))
					}
				}
			}
		}
	}

	valid := func(members []string) bool {
		in := map[string]bool{}
		for _, m := range members {
			in[m] = true
		}
		for _, m := range members {
			if tr.identityHasPlace(m) || tr.identityBorrowed(m) {
				return false
			}
			d, ok := defs[m]
			if !ok {
				return false
			}
			if d.param {
				for _, a := range incoming[m] {
					if in[a] {
						continue
					}
					// Entered only with something already a string.
					if _, err := strconv.Unquote(a); err != nil || !strings.HasPrefix(a, `"`) {
						return false
					}
				}
				if len(incoming[m]) == 0 {
					// A parameter nothing passes -- a handler's -- is not a
					// chain value.
					return false
				}
				continue
			}
			if !isConcat(m) {
				return false
			}
		}
		// One buffer may be every member at once, so at each append nothing
		// but the append's own result may still be live.
		for _, m := range members {
			a, ok := appendOf[m]
			if !ok {
				continue
			}
			for v := range after[ownPos{a.Block, a.Index}] {
				if in[v] && v != a.Result {
					return false
				}
			}
		}
		return true
	}
	chain := map[string]bool{}
	for _, members := range groups {
		if valid(members) {
			for _, m := range members {
				chain[m] = true
			}
		}
	}
	if len(chain) == 0 {
		return 0
	}

	rewritten := 0
	intrinsic := func(result *Value, name string, args ...string) Inst {
		return Inst{Result: result, Opcode: IntrinsicCall, Operands: []string{strconv.Quote(name)},
			ArgList: "(" + strings.Join(args, ", ") + ")"}
	}
	newValue := func(typ string) *Value {
		v := fn.newValue(typ)
		return &v
	}
	// snapshot reads each chain value an instruction mentions as a finished
	// string, and says what to call it instead.
	snapshot := func(out *[]Inst, text ...string) map[string]string {
		rename := map[string]string{}
		for _, t := range text {
			for _, tok := range ssaTokens(t) {
				if chain[tok] && rename[tok] == "" {
					v := newValue("*str")
					*out = append(*out, intrinsic(v, "strbuf.finish", tok))
					rename[tok] = v.String()
				}
			}
		}
		return rename
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for i, p := range b.Params {
			if t := ssaTokens(p); len(t) > 0 && chain[t[0]] {
				b.Params[i] = t[0] + ": *strbuf"
			}
		}
		var out []Inst
		for _, in := range b.Insts {
			res := ""
			if in.Result != nil {
				res = in.Result.String()
			}
			switch {
			case chain[res]:
				args := CallArgs(in)
				left, right := args[0], args[1]
				rename := snapshot(&out, right)
				right = replaceTokens(right, rename)
				result := &Value{N: in.Result.N, Type: "*strbuf"}
				if _, isAppend := appendOf[res]; isAppend && chain[left] {
					// The buffer the left operand is, extended in place.
					out = append(out, intrinsic(result, "strbuf.append", left, right))
				} else {
					// A chain's entry: a new buffer from the concatenation's
					// two operands, converted in the same order concat
					// converts them.
					rename := snapshot(&out, left)
					buf := newValue("*strbuf")
					out = append(out, intrinsic(buf, "strbuf.new", replaceTokens(left, rename)))
					out = append(out, intrinsic(result, "strbuf.append", buf.String(), right))
				}
				rewritten++
			case in.Opcode == ReleasePrev:
				// Names what a promoted local stopped holding; a string has
				// no destructor, and the emitter acts only on objects.
				out = append(out, in)
			default:
				rename := snapshot(&out, append(append([]string{in.ArgList}, in.Operands...), in.Attrs...)...)
				if len(rename) > 0 {
					for j := range in.Operands {
						in.Operands[j] = replaceTokens(in.Operands[j], rename)
					}
					for j := range in.Attrs {
						in.Attrs[j] = replaceTokens(in.Attrs[j], rename)
					}
					in.ArgList = replaceTokens(in.ArgList, rename)
				}
				out = append(out, in)
			}
		}
		b.Term = rewriteChainTerm(b.Term, blocks, chain, &out, intrinsic, newValue)
		b.Insts = out
	}
	return rewritten
}

// rewriteChainTerm keeps chain values flowing into chain parameters, enters a
// chain parameter from a string literal with a new buffer, and snapshots every
// other chain value the terminator reads.
func rewriteChainTerm(term string, blocks map[int]*Block, chain map[string]bool, out *[]Inst,
	intrinsic func(*Value, string, ...string) Inst, newValue func(string) *Value) string {
	isChainParam := func(block, i int) bool {
		b := blocks[block]
		if b == nil || i >= len(b.Params) {
			return false
		}
		p := ssaTokens(b.Params[i])
		return len(p) > 0 && chain[p[0]]
	}
	snap := map[string]string{}
	other := func(text string) string {
		for _, tok := range ssaTokens(text) {
			if chain[tok] && snap[tok] == "" {
				v := newValue("*str")
				*out = append(*out, intrinsic(v, "strbuf.finish", tok))
				snap[tok] = v.String()
			}
		}
		return replaceTokens(text, snap)
	}
	var b strings.Builder
	for i := 0; i < len(term); {
		// Copy up to the next `bbN(`, rewriting what lies between.
		j := nextTarget(term, i)
		if j < 0 {
			b.WriteString(other(term[i:]))
			break
		}
		open := strings.IndexByte(term[j:], '(') + j
		b.WriteString(other(term[i:j]))
		block, _ := strconv.Atoi(term[j+2 : open])
		end := matchParen(term, open)
		args := splitOwnArgs(term[open : end+1])
		for k, a := range args {
			a = strings.TrimSpace(a)
			switch {
			case isChainParam(block, k) && chain[a]:
			case isChainParam(block, k):
				v := newValue("*strbuf")
				*out = append(*out, intrinsic(v, "strbuf.new", a))
				a = v.String()
			default:
				a = other(a)
			}
			args[k] = a
		}
		b.WriteString(term[j:open] + "(" + strings.Join(args, ", ") + ")")
		i = end + 1
	}
	return b.String()
}

// nextTarget finds the next `bbN(` at or after i, outside a string literal.
func nextTarget(s string, i int) int {
	for ; i < len(s); i++ {
		if s[i] == '"' {
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
			continue
		}
		if !strings.HasPrefix(s[i:], "bb") || i > 0 && escapeTokenByte(s[i-1]) {
			continue
		}
		j := i + 2
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > i+2 && j < len(s) && s[j] == '(' {
			return i
		}
	}
	return -1
}

// matchParen is the index of the parenthesis closing the one at open.
func matchParen(s string, open int) int {
	depth := 0
	for k := open; k < len(s); k++ {
		switch s[k] {
		case '"':
			for k++; k < len(s) && s[k] != '"'; k++ {
				if s[k] == '\\' {
					k++
				}
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return len(s) - 1
}

// replaceTokens renames whole SSA tokens outside string literals.
func replaceTokens(s string, rename map[string]string) string {
	if len(rename) == 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c == '"' {
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(s))
			b.WriteString(s[i:j])
			i = j
			continue
		}
		if c == '%' {
			j := i + 1
			for j < len(s) && escapeTokenByte(s[j]) {
				j++
			}
			if r, ok := rename[s[i:j]]; ok {
				b.WriteString(r)
			} else {
				b.WriteString(s[i:j])
			}
			i = j
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}
