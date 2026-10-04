package stdlib

import "unicode/utf8"

// pcreBudget bounds the work of one match call. PHP's pcre.backtrack_limit is
// a million, counted by PCRE per backtracking point; this counts every node
// visited, which is never fewer, so a search PCRE would abandon as "Backtrack
// limit exhausted" runs out here first and is reported as unsupported rather
// than answered.
const pcreBudget = 1000000

type pcreMatcher struct {
	prog     *pcreProgram
	s        string
	caps     []int
	start    int // the search's start offset, for \G
	steps    int
	overflow bool
}

func (m *pcreMatcher) char(pos int) (rune, int) {
	if pos >= len(m.s) {
		return 0, 0
	}
	if !m.prog.utf {
		return rune(m.s[pos]), 1
	}
	return utf8.DecodeRuneInString(m.s[pos:])
}

func (m *pcreMatcher) prevChar(pos int) (rune, int) {
	if pos <= 0 {
		return 0, 0
	}
	if !m.prog.utf {
		return rune(m.s[pos-1]), 1
	}
	return utf8.DecodeLastRuneInString(m.s[:pos])
}

func (m *pcreMatcher) isWord(r rune) bool {
	if m.prog.utf {
		return isWordUCP(r)
	}
	return isWordASCII(r)
}

func (m *pcreMatcher) wordAt(pos int) bool {
	r, n := m.char(pos)
	return n > 0 && m.isWord(r)
}

func (m *pcreMatcher) wordBefore(pos int) bool {
	r, n := m.prevChar(pos)
	return n > 0 && m.isWord(r)
}

// run matches n at pos and calls k with the end position of each way it
// matches, in PCRE's priority order, until k accepts one.
func (m *pcreMatcher) run(n *reNode, pos int, k func(int) bool) bool {
	m.steps++
	if m.steps > pcreBudget {
		m.overflow = true
	}
	if m.overflow {
		return false
	}
	switch n.op {
	case reChar:
		r, size := m.char(pos)
		if size == 0 {
			return false
		}
		if r == n.ch || (n.fold && foldEqual(n.ch, r, m.prog.utf)) {
			return k(pos + size)
		}
		return false
	case reAny:
		r, size := m.char(pos)
		if size == 0 {
			return false
		}
		if r == '\n' && (n.noNewline || !n.dotall) {
			return false
		}
		return k(pos + size)
	case reSet:
		r, size := m.char(pos)
		if size == 0 || !n.cls.matches(r) {
			return false
		}
		return k(pos + size)
	case reCat:
		return m.seq(n.subs, 0, pos, k)
	case reAlt:
		for _, s := range n.subs {
			if m.run(s, pos, k) {
				return true
			}
			if m.overflow {
				return false
			}
		}
		return false
	case reGroup:
		g := n.group
		old0, old1 := m.caps[2*g], m.caps[2*g+1]
		ok := m.run(n.subs[0], pos, func(end int) bool {
			s0, s1 := m.caps[2*g], m.caps[2*g+1]
			m.caps[2*g], m.caps[2*g+1] = pos, end
			if k(end) {
				return true
			}
			m.caps[2*g], m.caps[2*g+1] = s0, s1
			return false
		})
		if !ok {
			m.caps[2*g], m.caps[2*g+1] = old0, old1
		}
		return ok
	case reRepeat:
		return m.repeat(n, 0, pos, k)
	case reAtomic:
		saved := append([]int(nil), m.caps...)
		end := -1
		if !m.run(n.subs[0], pos, func(e int) bool { end = e; return true }) {
			copy(m.caps, saved)
			return false
		}
		if k(end) {
			return true
		}
		copy(m.caps, saved)
		return false
	case reLook:
		saved := append([]int(nil), m.caps...)
		var found bool
		if n.behind {
			found = m.lookbehind(n, pos)
		} else {
			found = m.run(n.subs[0], pos, func(int) bool { return true })
		}
		if m.overflow {
			return false
		}
		if n.negate {
			copy(m.caps, saved)
			if found {
				return false
			}
			return k(pos)
		}
		if !found {
			copy(m.caps, saved)
			return false
		}
		if k(pos) {
			return true
		}
		copy(m.caps, saved)
		return false
	case reBackref:
		g := n.group
		if m.caps[2*g] < 0 {
			return false
		}
		ref := m.s[m.caps[2*g]:m.caps[2*g+1]]
		p := pos
		if !n.fold {
			if len(m.s)-p < len(ref) || m.s[p:p+len(ref)] != ref {
				return false
			}
			return k(p + len(ref))
		}
		for i := 0; i < len(ref); {
			var a rune
			var an int
			if m.prog.utf {
				a, an = utf8.DecodeRuneInString(ref[i:])
			} else {
				a, an = rune(ref[i]), 1
			}
			b, bn := m.char(p)
			if bn == 0 || !foldEqual(a, b, m.prog.utf) {
				return false
			}
			i += an
			p += bn
		}
		return k(p)
	case reBOL:
		if pos == 0 || (n.multiline && m.s[pos-1] == '\n' && pos < len(m.s)) {
			return k(pos)
		}
		return false
	case reEOL:
		switch {
		case pos == len(m.s):
		case n.multiline && m.s[pos] == '\n':
		case !n.multiline && !n.dollarEndOnly && pos == len(m.s)-1 && m.s[pos] == '\n':
		default:
			return false
		}
		return k(pos)
	case reStartSubject:
		if pos == 0 {
			return k(pos)
		}
		return false
	case reEndSubject:
		if pos == len(m.s) {
			return k(pos)
		}
		return false
	case reEndSubjectNL:
		if pos == len(m.s) || (pos == len(m.s)-1 && m.s[pos] == '\n') {
			return k(pos)
		}
		return false
	case reWordBoundary:
		if m.wordBefore(pos) != m.wordAt(pos) {
			return k(pos)
		}
		return false
	case reNotWordBoundary:
		if m.wordBefore(pos) == m.wordAt(pos) {
			return k(pos)
		}
		return false
	case reStartOffset:
		if pos == m.start {
			return k(pos)
		}
		return false
	}
	return false
}

func (m *pcreMatcher) seq(subs []*reNode, i, pos int, k func(int) bool) bool {
	if i == len(subs) {
		return k(pos)
	}
	return m.run(subs[i], pos, func(p int) bool { return m.seq(subs, i+1, p, k) })
}

// repeat is a quantifier. An iteration that matches the empty string once
// the minimum is met ends the loop -- PCRE's guard against looping forever
// on `(a?)*` -- and matching goes on after it.
func (m *pcreMatcher) repeat(n *reNode, count, pos int, k func(int) bool) bool {
	canMore := n.max < 0 || count < n.max
	more := func() bool {
		return m.run(n.subs[0], pos, func(p int) bool {
			if p == pos && count+1 > n.min {
				return k(p)
			}
			return m.repeat(n, count+1, p, k)
		})
	}
	if count < n.min {
		return canMore && more()
	}
	if n.greedy {
		if canMore && more() {
			return true
		}
		if m.overflow {
			return false
		}
		return k(pos)
	}
	if k(pos) {
		return true
	}
	if m.overflow {
		return false
	}
	return canMore && more()
}

// lookbehind tries each fixed-length branch ending exactly at pos.
func (m *pcreMatcher) lookbehind(n *reNode, pos int) bool {
	for _, b := range lookBranches(n) {
		length, _ := fixedLength(b)
		start := pos
		ok := true
		for i := 0; i < length; i++ {
			_, size := m.prevChar(start)
			if size == 0 {
				ok = false
				break
			}
			start -= size
		}
		if !ok {
			continue
		}
		if m.run(b, start, func(e int) bool { return e == pos }) {
			return true
		}
		if m.overflow {
			return false
		}
	}
	return false
}

// pcreResult is one match: the ovector (pairs, -1 for unset) and PCRE's
// return count -- the highest set group plus one.
type pcreResult struct {
	ovector []int
	count   int
}

// exec is pcre2_match: search from start for the leftmost match. anchored
// limits it to start; notEmptyAtStart refuses an empty match there. An error
// is an overflow of the budget.
func (prog *pcreProgram) exec(s string, start int, anchored, notEmptyAtStart bool) (*pcreResult, error) {
	m := &pcreMatcher{prog: prog, s: s, caps: make([]int, 2*(prog.groups+1)), start: start}
	anchored = anchored || prog.anchored
	for p := start; p <= len(s); {
		for i := range m.caps {
			m.caps[i] = -1
		}
		end := -1
		if m.run(prog.root, p, func(e int) bool {
			if notEmptyAtStart && e == p && p == start {
				return false
			}
			end = e
			return true
		}) {
			m.caps[0], m.caps[1] = p, end
			count := 1
			for g := prog.groups; g >= 1; g-- {
				if m.caps[2*g] >= 0 {
					count = g + 1
					break
				}
			}
			return &pcreResult{ovector: m.caps, count: count}, nil
		}
		if m.overflow {
			return nil, refuse("preg match needing more backtracking than php2go's budget, whose outcome depends on pcre.backtrack_limit")
		}
		if anchored || p == len(s) {
			break
		}
		p += prog.unitLen(s, p)
	}
	return nil, nil
}

// unitLen is the length of the character at pos: one byte, or a code point
// under /u.
func (prog *pcreProgram) unitLen(s string, pos int) int {
	if !prog.utf || pos >= len(s) {
		return 1
	}
	_, n := utf8.DecodeRuneInString(s[pos:])
	return n
}
