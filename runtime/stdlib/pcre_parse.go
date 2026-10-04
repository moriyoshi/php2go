package stdlib

// A PCRE2 pattern compiler for the preg_* family.
//
// **It is a backtracking matcher, not a translation to Go's regexp.** RE2
// agrees with PCRE on which strings match but not on *how*: PCRE reports the
// first match its depth-first search finds, alternatives and quantifiers tried
// in priority order, and that choice is visible in every capture group and in
// every replacement. A matcher that walks the same search tree in the same
// order gives the same answer by construction, and it can host the lookaround,
// backreference and atomic constructs RE2 has no answer for.
//
// **What it cannot model it refuses.** Anything outside the supported
// subset -- recursion, conditionals, branch reset, backtracking verbs, `\K`,
// `\X`, variable-length lookbehind, an option letter it does not know -- is
// reported as unsupported rather than approximated, and so is a pattern PCRE
// itself would reject: its "Compilation failed: ... at offset N" wording is
// PCRE's own and is not reproduced here.

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type reOp int

const (
	reChar reOp = iota
	reAny
	reSet
	reCat
	reAlt
	reRepeat
	reGroup
	reAtomic
	reLook
	reBackref
	reBOL
	reEOL
	reStartSubject
	reEndSubject
	reEndSubjectNL
	reWordBoundary
	reNotWordBoundary
	reStartOffset
)

type reNode struct {
	op   reOp
	ch   rune
	fold bool
	cls  *reClass
	subs []*reNode
	// reRepeat
	min, max int
	greedy   bool
	// reGroup: the capture index; reBackref: the group referred to.
	group int
	// reLook
	negate, behind bool
	// reAny
	dotall bool
	// reBOL / reEOL
	multiline, dollarEndOnly bool
	// reAny under \N: never a newline, whatever /s says.
	noNewline bool
}

// reClass is a character class: ranges and predicates, optionally negated,
// optionally caseless.
type reClass struct {
	negate bool
	ranges [][2]rune
	preds  []func(rune) bool
	fold   bool
	utf    bool
}

func (c *reClass) raw(r rune) bool {
	for _, rg := range c.ranges {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	for _, p := range c.preds {
		if p(r) {
			return true
		}
	}
	return false
}

func (c *reClass) matches(r rune) bool {
	in := c.raw(r)
	if !in && c.fold {
		for f := foldNext(r, c.utf); f != r; f = foldNext(f, c.utf) {
			if c.raw(f) {
				in = true
				break
			}
		}
	}
	return in != c.negate
}

// foldNext walks a character's case orbit. Without /u PCRE uses its default
// C-locale tables, which fold ASCII letters only (probed: `/\xc9/i` does not
// match "\xe9"); with /u it folds by Unicode.
func foldNext(r rune, utf bool) rune {
	if !utf {
		switch {
		case r >= 'a' && r <= 'z':
			return r - 32
		case r >= 'A' && r <= 'Z':
			return r + 32
		}
		return r
	}
	return unicode.SimpleFold(r)
}

func foldEqual(a, b rune, utf bool) bool {
	if a == b {
		return true
	}
	for f := foldNext(a, utf); f != a; f = foldNext(f, utf) {
		if f == b {
			return true
		}
	}
	return false
}

// reFlags are the options in force at one point of a pattern.
type reFlags struct {
	caseless, multiline, dotall, extended, ungreedy bool
}

// pcreProgram is one compiled pattern.
type pcreProgram struct {
	root          *reNode
	groups        int // capture groups, not counting group 0
	names         map[int]string
	utf           bool
	anchored      bool
	dollarEndOnly bool
}

type reUnsupported struct{ why string }

func (e *reUnsupported) Error() string { return e.why }

type reParser struct {
	src           []rune // the pattern, as code points (bytes without /u)
	pos           int
	utf           bool
	dollarEndOnly bool
	groups        int
	names         map[string]int
	byIndex       map[int]string
	backrefs      []*reNode
	namedRefs     map[*reNode]string
}

func refuse(format string, args ...any) error {
	return &reUnsupported{why: fmt.Sprintf(format, args...)}
}

// compilePCRE compiles a pattern body (delimiters and modifiers already split
// off). An error is always a refusal: see the file comment.
func compilePCRE(body string, mods pcreModifiers) (*pcreProgram, error) {
	p := &reParser{utf: mods.utf, dollarEndOnly: mods.dollarEndOnly, names: map[string]int{}, byIndex: map[int]string{}, namedRefs: map[*reNode]string{}}
	if mods.utf {
		if !utf8.ValidString(body) {
			return nil, refuse("preg pattern that is not valid UTF-8 under /u")
		}
		p.src = []rune(body)
	} else {
		p.src = make([]rune, len(body))
		for i := 0; i < len(body); i++ {
			p.src[i] = rune(body[i])
		}
	}
	if strings.HasPrefix(body, "(*") {
		return nil, refuse("preg pattern starting with a (*VERB) option")
	}
	flags := reFlags{caseless: mods.caseless, multiline: mods.multiline, dotall: mods.dotall, extended: mods.extended, ungreedy: mods.ungreedy}
	root, err := p.parseAlt(&flags, 0)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.src) {
		// A stray `)`: PCRE's "unmatched closing parenthesis".
		return nil, refuse("preg pattern PCRE would not compile (unmatched parenthesis)")
	}
	for _, ref := range p.backrefs {
		if name, named := p.namedRefs[ref]; named {
			idx, ok := p.names[name]
			if !ok {
				return nil, refuse("preg pattern PCRE would not compile (reference to unknown group name)")
			}
			ref.group = idx
		}
		if ref.group < 1 || ref.group > p.groups {
			return nil, refuse("preg pattern PCRE would not compile (reference to non-existent group)")
		}
	}
	if err := checkLookbehinds(root, p.utf); err != nil {
		return nil, err
	}
	return &pcreProgram{root: root, groups: p.groups, names: p.byIndex, utf: mods.utf, anchored: mods.anchored, dollarEndOnly: mods.dollarEndOnly}, nil
}

func (p *reParser) more() bool { return p.pos < len(p.src) }
func (p *reParser) peek() rune { return p.src[p.pos] }
func (p *reParser) peekAt(i int) (rune, bool) {
	if p.pos+i < len(p.src) {
		return p.src[p.pos+i], true
	}
	return 0, false
}

// skipExtended skips /x whitespace and comments.
func (p *reParser) skipExtended(f *reFlags) {
	if !f.extended {
		return
	}
	for p.more() {
		c := p.peek()
		if c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r' {
			p.pos++
			continue
		}
		if c == '#' {
			for p.more() && p.peek() != '\n' {
				p.pos++
			}
			continue
		}
		return
	}
}

// parseAlt parses alternatives up to a `)` or the end. The flags are shared by
// the branches: an option set in one alternative carries into the ones after
// it within the same group, as PCRE documents.
func (p *reParser) parseAlt(f *reFlags, depth int) (*reNode, error) {
	var branches []*reNode
	for {
		seq, err := p.parseSeq(f, depth)
		if err != nil {
			return nil, err
		}
		branches = append(branches, seq)
		if p.more() && p.peek() == '|' {
			p.pos++
			continue
		}
		break
	}
	if len(branches) == 1 {
		return branches[0], nil
	}
	return &reNode{op: reAlt, subs: branches}, nil
}

func (p *reParser) parseSeq(f *reFlags, depth int) (*reNode, error) {
	seq := &reNode{op: reCat}
	for {
		p.skipExtended(f)
		if !p.more() {
			break
		}
		c := p.peek()
		if c == '|' {
			break
		}
		if c == ')' {
			if depth == 0 {
				return nil, refuse("preg pattern PCRE would not compile (unmatched closing parenthesis)")
			}
			break
		}
		atom, quantifiable, err := p.parseAtom(f, depth)
		if err != nil {
			return nil, err
		}
		if atom == nil {
			continue // an option setting or a comment
		}
		p.skipExtended(f)
		atom, err = p.parseQuantifier(atom, quantifiable, f)
		if err != nil {
			return nil, err
		}
		seq.subs = append(seq.subs, atom)
	}
	if len(seq.subs) == 1 {
		return seq.subs[0], nil
	}
	return seq, nil
}

// parseQuantifier applies a following quantifier, if any.
func (p *reParser) parseQuantifier(atom *reNode, quantifiable bool, f *reFlags) (*reNode, error) {
	if !p.more() {
		return atom, nil
	}
	min, max := -1, -1
	start := p.pos
	switch p.peek() {
	case '*':
		min, max = 0, -1
		p.pos++
	case '+':
		min, max = 1, -1
		p.pos++
	case '?':
		min, max = 0, 1
		p.pos++
	case '{':
		lo, hi, ok := p.braceQuantifier()
		if !ok {
			return atom, nil // a literal brace
		}
		min, max = lo, hi
	default:
		return atom, nil
	}
	if !quantifiable {
		return nil, refuse("preg pattern with a quantified assertion or nothing to repeat")
	}
	if max >= 0 && min > max {
		p.pos = start
		return nil, refuse("preg pattern PCRE would not compile (numbers out of order in {} quantifier)")
	}
	greedy := !f.ungreedy
	possessive := false
	if p.more() {
		switch p.peek() {
		case '?':
			greedy = !greedy
			p.pos++
		case '+':
			possessive = true
			greedy = true
			p.pos++
		}
	}
	if p.more() {
		switch p.peek() {
		case '*', '+', '?':
			return nil, refuse("preg pattern with a doubled quantifier")
		case '{':
			save := p.pos
			if _, _, ok := p.braceQuantifier(); ok {
				return nil, refuse("preg pattern with a doubled quantifier")
			}
			p.pos = save
		}
	}
	rep := &reNode{op: reRepeat, subs: []*reNode{atom}, min: min, max: max, greedy: greedy}
	if possessive {
		return &reNode{op: reAtomic, subs: []*reNode{rep}}, nil
	}
	return rep, nil
}

// braceQuantifier reads `{n}`, `{n,}`, `{n,m}` or `{,m}` (PCRE2 10.43 and
// later take the last one as a quantifier: probed, `/a{,2}/` matches "a"). A
// brace that is not one of those is a literal, and the position is left alone.
func (p *reParser) braceQuantifier() (int, int, bool) {
	save := p.pos
	p.pos++ // {
	readNum := func() (int, bool) {
		start := p.pos
		for p.more() && p.peek() >= '0' && p.peek() <= '9' {
			p.pos++
		}
		if p.pos == start {
			return 0, false
		}
		n, err := strconv.Atoi(string(p.src[start:p.pos]))
		if err != nil || n > 65535 {
			return 0, false
		}
		return n, true
	}
	lo, hasLo := readNum()
	if !p.more() {
		p.pos = save
		return 0, 0, false
	}
	if p.peek() == '}' {
		if !hasLo {
			p.pos = save
			return 0, 0, false
		}
		p.pos++
		return lo, lo, true
	}
	if p.peek() != ',' {
		p.pos = save
		return 0, 0, false
	}
	p.pos++
	hi, hasHi := readNum()
	if !p.more() || p.peek() != '}' || (!hasLo && !hasHi) {
		p.pos = save
		return 0, 0, false
	}
	p.pos++
	if !hasLo {
		lo = 0
	}
	if !hasHi {
		hi = -1
	}
	return lo, hi, true
}

// parseAtom reads one atom. quantifiable is false for assertions.
func (p *reParser) parseAtom(f *reFlags, depth int) (*reNode, bool, error) {
	c := p.peek()
	switch c {
	case '(':
		return p.parseGroup(f, depth)
	case '[':
		cls, err := p.parseClass(f)
		if err != nil {
			return nil, false, err
		}
		return &reNode{op: reSet, cls: cls}, true, nil
	case '.':
		p.pos++
		return &reNode{op: reAny, dotall: f.dotall}, true, nil
	case '^':
		p.pos++
		return &reNode{op: reBOL, multiline: f.multiline}, false, nil
	case '$':
		p.pos++
		return &reNode{op: reEOL, multiline: f.multiline, dollarEndOnly: p.dollarEndOnly}, false, nil
	case '\\':
		return p.parseEscape(f)
	case '*', '+', '?':
		return nil, false, refuse("preg pattern PCRE would not compile (quantifier does not follow a repeatable item)")
	case '{':
		save := p.pos
		if _, _, ok := p.braceQuantifier(); ok {
			return nil, false, refuse("preg pattern PCRE would not compile (quantifier does not follow a repeatable item)")
		}
		p.pos = save
	}
	p.pos++
	return &reNode{op: reChar, ch: c, fold: f.caseless}, true, nil
}

func (p *reParser) parseGroup(f *reFlags, depth int) (*reNode, bool, error) {
	p.pos++ // (
	if p.more() && p.peek() == '*' {
		return nil, false, refuse("preg pattern with a (*VERB)")
	}
	if !p.more() || p.peek() != '?' {
		p.groups++
		idx := p.groups
		inner := *f
		sub, err := p.parseAlt(&inner, depth+1)
		if err != nil {
			return nil, false, err
		}
		if err := p.expect(')'); err != nil {
			return nil, false, err
		}
		return &reNode{op: reGroup, group: idx, subs: []*reNode{sub}}, true, nil
	}
	p.pos++ // ?
	if !p.more() {
		return nil, false, refuse("preg pattern PCRE would not compile (unterminated group)")
	}
	c := p.peek()
	switch c {
	case '#':
		for p.more() && p.peek() != ')' {
			p.pos++
		}
		if err := p.expect(')'); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	case ':':
		p.pos++
		return p.groupBody(f, depth, &reNode{op: reCat}, true)
	case '>':
		p.pos++
		return p.groupBody(f, depth, &reNode{op: reAtomic}, true)
	case '=', '!':
		p.pos++
		return p.groupBody(f, depth, &reNode{op: reLook, negate: c == '!'}, false)
	case '<':
		if n, ok := p.peekAt(1); ok && (n == '=' || n == '!') {
			p.pos += 2
			return p.groupBody(f, depth, &reNode{op: reLook, behind: true, negate: n == '!'}, false)
		}
		p.pos++
		return p.namedGroup(f, depth, '>')
	case '\'':
		p.pos++
		return p.namedGroup(f, depth, '\'')
	case 'P':
		n, _ := p.peekAt(1)
		switch n {
		case '<':
			p.pos += 2
			return p.namedGroup(f, depth, '>')
		case '=':
			p.pos += 2
			name, err := p.readName(')')
			if err != nil {
				return nil, false, err
			}
			ref := &reNode{op: reBackref, fold: f.caseless}
			p.namedRefs[ref] = name
			p.backrefs = append(p.backrefs, ref)
			return ref, true, nil
		}
		return nil, false, refuse("preg pattern with a (?P> recursion")
	}
	// Option settings: (?imsxU-imsxU) or (?imsxU-imsxU:...).
	on := true
	nf := *f
	for p.more() {
		c := p.peek()
		switch c {
		case 'i':
			nf.caseless = on
		case 'm':
			nf.multiline = on
		case 's':
			nf.dotall = on
		case 'x':
			nf.extended = on
		case 'U':
			nf.ungreedy = on
		case '-':
			if !on {
				return nil, false, refuse("preg pattern PCRE would not compile (bad option setting)")
			}
			on = false
		case ')':
			p.pos++
			*f = nf
			return nil, false, nil
		case ':':
			p.pos++
			return p.groupBody(&nf, depth, &reNode{op: reCat}, true)
		default:
			return nil, false, refuse("preg pattern with the (?%c construct", c)
		}
		p.pos++
	}
	return nil, false, refuse("preg pattern PCRE would not compile (unterminated group)")
}

// groupBody parses the alternatives of a non-capturing construct into shell.
func (p *reParser) groupBody(f *reFlags, depth int, shell *reNode, quantifiable bool) (*reNode, bool, error) {
	inner := *f
	sub, err := p.parseAlt(&inner, depth+1)
	if err != nil {
		return nil, false, err
	}
	if err := p.expect(')'); err != nil {
		return nil, false, err
	}
	if shell.op == reCat {
		return sub, true, nil
	}
	shell.subs = []*reNode{sub}
	return shell, quantifiable, nil
}

func (p *reParser) namedGroup(f *reFlags, depth int, close rune) (*reNode, bool, error) {
	name, err := p.readName(close)
	if err != nil {
		return nil, false, err
	}
	if _, dup := p.names[name]; dup {
		return nil, false, refuse("preg pattern with a duplicate group name")
	}
	p.groups++
	idx := p.groups
	p.names[name] = idx
	p.byIndex[idx] = name
	inner := *f
	sub, err := p.parseAlt(&inner, depth+1)
	if err != nil {
		return nil, false, err
	}
	if err := p.expect(')'); err != nil {
		return nil, false, err
	}
	return &reNode{op: reGroup, group: idx, subs: []*reNode{sub}}, true, nil
}

// readName reads a group name up to its terminator. PCRE names are word
// characters not starting with a digit.
func (p *reParser) readName(close rune) (string, error) {
	start := p.pos
	for p.more() && p.peek() != close {
		c := p.peek()
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return "", refuse("preg pattern with a group name PCRE would refuse or read differently")
		}
		p.pos++
	}
	if !p.more() || p.pos == start || (p.src[start] >= '0' && p.src[start] <= '9') {
		return "", refuse("preg pattern PCRE would not compile (bad group name)")
	}
	name := string(p.src[start:p.pos])
	p.pos++
	return name, nil
}

func (p *reParser) expect(c rune) error {
	if !p.more() || p.peek() != c {
		return refuse("preg pattern PCRE would not compile (missing closing parenthesis)")
	}
	p.pos++
	return nil
}

// parseEscape reads a backslash sequence outside a class.
func (p *reParser) parseEscape(f *reFlags) (*reNode, bool, error) {
	p.pos++ // backslash
	if !p.more() {
		return nil, false, refuse("preg pattern PCRE would not compile (\\ at end of pattern)")
	}
	c := p.peek()
	switch c {
	case 'A':
		p.pos++
		return &reNode{op: reStartSubject}, false, nil
	case 'z':
		p.pos++
		return &reNode{op: reEndSubject}, false, nil
	case 'Z':
		p.pos++
		return &reNode{op: reEndSubjectNL}, false, nil
	case 'b':
		p.pos++
		return &reNode{op: reWordBoundary}, false, nil
	case 'B':
		p.pos++
		return &reNode{op: reNotWordBoundary}, false, nil
	case 'G':
		p.pos++
		return &reNode{op: reStartOffset}, false, nil
	case 'N':
		p.pos++
		if p.more() && p.peek() == '{' {
			return nil, false, refuse("preg pattern with \\N{...}")
		}
		return &reNode{op: reAny, noNewline: true}, true, nil
	case 'R':
		p.pos++
		return p.newlineSequence(), true, nil
	case 'Q':
		p.pos++
		seq := &reNode{op: reCat}
		for p.more() {
			if p.peek() == '\\' {
				if n, ok := p.peekAt(1); ok && n == 'E' {
					p.pos += 2
					break
				}
			}
			seq.subs = append(seq.subs, &reNode{op: reChar, ch: p.peek(), fold: f.caseless})
			p.pos++
		}
		return seq, len(seq.subs) > 0, nil
	case 'E':
		p.pos++
		return nil, false, nil
	case 'g', 'k':
		return p.parseBackrefEscape(f)
	case 'K', 'X', 'C':
		return nil, false, refuse("preg pattern with \\%c", c)
	}
	if c >= '1' && c <= '9' {
		// PCRE2's rule: the digits are one decimal number, a backreference
		// when it is below 10, starts with 8 or 9, or names a group already
		// opened; otherwise up to three octal digits are a character.
		start := p.pos
		n, _ := p.readDigits()
		if n < 10 || c == '8' || c == '9' || n <= p.groups {
			ref := &reNode{op: reBackref, group: n, fold: f.caseless}
			p.backrefs = append(p.backrefs, ref)
			return ref, true, nil
		}
		p.pos = start
		v := 0
		for i := 0; i < 3 && p.more() && p.peek() >= '0' && p.peek() <= '7'; i++ {
			v = v*8 + int(p.peek()-'0')
			p.pos++
		}
		r, err := p.checkCode(rune(v))
		if err != nil {
			return nil, false, err
		}
		return &reNode{op: reChar, ch: r, fold: f.caseless}, true, nil
	}
	if pred, ok := p.classEscape(c); ok {
		p.pos++
		return &reNode{op: reSet, cls: &reClass{preds: []func(rune) bool{pred}, utf: p.utf}}, true, nil
	}
	if c == 'p' || c == 'P' {
		pred, err := p.property()
		if err != nil {
			return nil, false, err
		}
		return &reNode{op: reSet, cls: &reClass{preds: []func(rune) bool{pred}, utf: p.utf, fold: f.caseless}}, true, nil
	}
	r, err := p.charEscape(false)
	if err != nil {
		return nil, false, err
	}
	return &reNode{op: reChar, ch: r, fold: f.caseless}, true, nil
}

// newlineSequence is \R: (?>\r\n|\n|\x0b|\f|\r|\x85), plus U+2028 and U+2029
// under /u.
func (p *reParser) newlineSequence() *reNode {
	crlf := &reNode{op: reCat, subs: []*reNode{{op: reChar, ch: '\r'}, {op: reChar, ch: '\n'}}}
	single := &reClass{ranges: [][2]rune{{'\n', '\r'}, {0x85, 0x85}}, utf: p.utf}
	if p.utf {
		single.ranges = append(single.ranges, [2]rune{0x2028, 0x2029})
	}
	alt := &reNode{op: reAlt, subs: []*reNode{crlf, {op: reSet, cls: single}}}
	return &reNode{op: reAtomic, subs: []*reNode{alt}}
}

func (p *reParser) parseBackrefEscape(f *reFlags) (*reNode, bool, error) {
	kind := p.peek()
	p.pos++
	ref := &reNode{op: reBackref, fold: f.caseless}
	if !p.more() {
		return nil, false, refuse("preg pattern PCRE would not compile (\\%c at end)", kind)
	}
	c := p.peek()
	var close rune
	switch c {
	case '{':
		close = '}'
	case '<':
		close = '>'
	case '\'':
		close = '\''
	}
	if kind == 'g' && close == 0 {
		// \gN or \g-N
		neg := false
		if c == '-' {
			neg = true
			p.pos++
		} else if c == '+' {
			return nil, false, refuse("preg pattern with \\g+ (a subroutine call)")
		}
		n, ok := p.readDigits()
		if !ok {
			return nil, false, refuse("preg pattern PCRE would not compile (bad \\g reference)")
		}
		if neg {
			n = p.groups - n + 1
		}
		ref.group = n
		p.backrefs = append(p.backrefs, ref)
		return ref, true, nil
	}
	if close == 0 {
		return nil, false, refuse("preg pattern PCRE would not compile (bad \\k reference)")
	}
	if kind == 'g' && (c == '<' || c == '\'') {
		return nil, false, refuse("preg pattern with a \\g subroutine call")
	}
	p.pos++
	if kind == 'g' && p.more() && (p.peek() == '-' || p.peek() >= '0' && p.peek() <= '9') {
		neg := p.peek() == '-'
		if neg {
			p.pos++
		}
		n, ok := p.readDigits()
		if !ok || !p.more() || p.peek() != close {
			return nil, false, refuse("preg pattern PCRE would not compile (bad \\g reference)")
		}
		p.pos++
		if neg {
			n = p.groups - n + 1
		}
		ref.group = n
		p.backrefs = append(p.backrefs, ref)
		return ref, true, nil
	}
	name, err := p.readName(close)
	if err != nil {
		return nil, false, err
	}
	p.namedRefs[ref] = name
	p.backrefs = append(p.backrefs, ref)
	return ref, true, nil
}

func (p *reParser) readDigits() (int, bool) {
	start := p.pos
	for p.more() && p.peek() >= '0' && p.peek() <= '9' {
		p.pos++
	}
	if p.pos == start {
		return 0, false
	}
	n, err := strconv.Atoi(string(p.src[start:p.pos]))
	return n, err == nil
}

// classEscape answers the shorthand classes. With /u PHP also sets
// PCRE2_UCP, so they follow Unicode properties there (probed: `\w` matches
// U+0301 and U+203F, not U+0903 or U+20DD; `\s` matches U+00A0).
func (p *reParser) classEscape(c rune) (func(rune) bool, bool) {
	var base func(rune) bool
	switch c {
	case 'd', 'D':
		base = isDigitASCII
		if p.utf {
			base = func(r rune) bool { return unicode.Is(unicode.Nd, r) }
		}
	case 'w', 'W':
		base = isWordASCII
		if p.utf {
			base = isWordUCP
		}
	case 's', 'S':
		base = isSpaceASCII
		if p.utf {
			base = func(r rune) bool { return unicode.Is(unicode.Z, r) || isHSpace(r) || isVSpace(r) }
		}
	case 'h', 'H':
		base = isHSpace
	case 'v', 'V':
		base = isVSpace
	default:
		return nil, false
	}
	if c >= 'A' && c <= 'Z' {
		return func(r rune) bool { return !base(r) }, true
	}
	return base, true
}

func isDigitASCII(r rune) bool { return r >= '0' && r <= '9' }
func isWordASCII(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}
func isWordUCP(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Pc, r)
}
func isSpaceASCII(r rune) bool {
	return r == ' ' || r >= '\t' && r <= '\r'
}
func isHSpace(r rune) bool {
	switch r {
	case '\t', ' ', 0xa0, 0x1680, 0x180e, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}
func isVSpace(r rune) bool {
	return r >= '\n' && r <= '\r' || r == 0x85 || r == 0x2028 || r == 0x2029
}

// property reads \p{..} / \P{..} / \pL. Only exact general-category and
// script names are known; anything else is refused.
func (p *reParser) property() (func(rune) bool, error) {
	negate := p.peek() == 'P'
	p.pos++
	if !p.more() {
		return nil, refuse("preg pattern PCRE would not compile (malformed \\p)")
	}
	var name string
	if p.peek() == '{' {
		p.pos++
		start := p.pos
		for p.more() && p.peek() != '}' {
			p.pos++
		}
		if !p.more() {
			return nil, refuse("preg pattern PCRE would not compile (malformed \\p)")
		}
		name = string(p.src[start:p.pos])
		p.pos++
	} else {
		name = string(p.peek())
		p.pos++
	}
	if strings.HasPrefix(name, "^") {
		negate = !negate
		name = name[1:]
	}
	var pred func(rune) bool
	switch name {
	case "Any":
		pred = func(rune) bool { return true }
	case "L&", "LC":
		pred = func(r rune) bool { return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lt) }
	default:
		if t, ok := unicode.Categories[name]; ok {
			pred = func(r rune) bool { return unicode.Is(t, r) }
		} else if t, ok := unicode.Scripts[name]; ok {
			pred = func(r rune) bool { return unicode.Is(t, r) }
		} else {
			return nil, refuse("preg pattern with the Unicode property \\p{%s}", name)
		}
	}
	if negate {
		return func(r rune) bool { return !pred(r) }, nil
	}
	return pred, nil
}

// charEscape reads an escape that stands for one character. inClass
// changes \b to backspace.
func (p *reParser) charEscape(inClass bool) (rune, error) {
	c := p.peek()
	p.pos++
	switch c {
	case 'a':
		return 7, nil
	case 'e':
		return 27, nil
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'b':
		if inClass {
			return 8, nil
		}
	case '0':
		n := 0
		for i := 0; i < 2 && p.more() && p.peek() >= '0' && p.peek() <= '7'; i++ {
			n = n*8 + int(p.peek()-'0')
			p.pos++
		}
		return rune(n), nil
	case 'o':
		if !p.more() || p.peek() != '{' {
			return 0, refuse("preg pattern PCRE would not compile (malformed \\o)")
		}
		p.pos++
		start := p.pos
		for p.more() && p.peek() != '}' {
			p.pos++
		}
		if !p.more() {
			return 0, refuse("preg pattern PCRE would not compile (malformed \\o)")
		}
		n, err := strconv.ParseInt(string(p.src[start:p.pos]), 8, 32)
		p.pos++
		if err != nil {
			return 0, refuse("preg pattern PCRE would not compile (malformed \\o)")
		}
		return p.checkCode(rune(n))
	case 'x':
		if p.more() && p.peek() == '{' {
			p.pos++
			start := p.pos
			for p.more() && p.peek() != '}' {
				p.pos++
			}
			if !p.more() || p.pos == start {
				return 0, refuse("preg pattern with a malformed \\x{...}")
			}
			n, err := strconv.ParseInt(string(p.src[start:p.pos]), 16, 32)
			p.pos++
			if err != nil {
				return 0, refuse("preg pattern with a malformed \\x{...}")
			}
			return p.checkCode(rune(n))
		}
		n := 0
		for i := 0; i < 2 && p.more() && isHexDigit(p.peek()); i++ {
			n = n*16 + hexValue(p.peek())
			p.pos++
		}
		return rune(n), nil
	case 'c':
		if !p.more() || p.peek() > 127 {
			return 0, refuse("preg pattern PCRE would not compile (malformed \\c)")
		}
		x := p.peek()
		p.pos++
		if x >= 'a' && x <= 'z' {
			x -= 32
		}
		return x ^ 0x40, nil
	}
	if c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
		return 0, refuse("preg pattern with the escape \\%c", c)
	}
	return c, nil
}

func (p *reParser) checkCode(r rune) (rune, error) {
	if !p.utf && r > 255 {
		return 0, refuse("preg pattern PCRE would not compile (character code point value too large)")
	}
	if p.utf && (r > unicode.MaxRune || r >= 0xd800 && r <= 0xdfff) {
		return 0, refuse("preg pattern PCRE would not compile (invalid code point)")
	}
	return r, nil
}

func isHexDigit(r rune) bool {
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

func hexValue(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	}
	return int(r-'A') + 10
}

// parseClass reads a bracketed character class.
func (p *reParser) parseClass(f *reFlags) (*reClass, error) {
	p.pos++ // [
	cls := &reClass{fold: f.caseless, utf: p.utf}
	if p.more() && p.peek() == '^' {
		cls.negate = true
		p.pos++
	}
	first := true
	for {
		if !p.more() {
			return nil, refuse("preg pattern PCRE would not compile (missing terminating ] for character class)")
		}
		c := p.peek()
		if c == ']' && !first {
			p.pos++
			return cls, nil
		}
		first = false
		// POSIX classes.
		if c == '[' {
			if n, ok := p.peekAt(1); ok && (n == ':' || n == '.' || n == '=') {
				pred, err := p.posixClass()
				if err != nil {
					return nil, err
				}
				cls.preds = append(cls.preds, pred)
				continue
			}
		}
		lo, isChar, err := p.classAtom(cls)
		if err != nil {
			return nil, err
		}
		if !isChar {
			continue
		}
		// A range?
		if p.more() && p.peek() == '-' {
			if n, ok := p.peekAt(1); ok && n != ']' {
				save := p.pos
				p.pos++
				if p.peek() == '[' {
					if n2, ok := p.peekAt(1); ok && (n2 == ':' || n2 == '.' || n2 == '=') {
						return nil, refuse("preg pattern with a range ending in a POSIX class")
					}
				}
				hi, hiChar, err := p.classAtom(cls)
				if err != nil {
					return nil, err
				}
				if !hiChar {
					p.pos = save
					return nil, refuse("preg pattern with a range ending in a class escape")
				}
				if hi < lo {
					return nil, refuse("preg pattern PCRE would not compile (range out of order in character class)")
				}
				cls.ranges = append(cls.ranges, [2]rune{lo, hi})
				continue
			}
		}
		cls.ranges = append(cls.ranges, [2]rune{lo, lo})
	}
}

// classAtom reads one member: a character (isChar) or a predicate it has
// already added to cls.
func (p *reParser) classAtom(cls *reClass) (rune, bool, error) {
	c := p.peek()
	if c != '\\' {
		p.pos++
		return c, true, nil
	}
	p.pos++
	if !p.more() {
		return 0, false, refuse("preg pattern PCRE would not compile (\\ at end of pattern)")
	}
	e := p.peek()
	if pred, ok := p.classEscape(e); ok {
		p.pos++
		cls.preds = append(cls.preds, pred)
		return 0, false, nil
	}
	switch e {
	case 'p', 'P':
		pred, err := p.property()
		if err != nil {
			return 0, false, err
		}
		cls.preds = append(cls.preds, pred)
		return 0, false, nil
	case 'Q':
		p.pos++
		// \Q...\E inside a class: each character literal.
		for p.more() {
			if p.peek() == '\\' {
				if n, ok := p.peekAt(1); ok && n == 'E' {
					p.pos += 2
					break
				}
			}
			cls.ranges = append(cls.ranges, [2]rune{p.peek(), p.peek()})
			p.pos++
		}
		return 0, false, nil
	case 'E':
		p.pos++
		return 0, false, nil
	case 'N', 'R', 'X', 'B', 'g', 'k', 'K', 'A', 'z', 'Z', 'G', 'C':
		return 0, false, refuse("preg pattern with \\%c in a character class", e)
	}
	if e >= '1' && e <= '9' {
		return 0, false, refuse("preg pattern with an octal escape in a character class")
	}
	r, err := p.charEscape(true)
	return r, true, err
}

// posixClass reads [:name:] or [:^name:].
func (p *reParser) posixClass() (func(rune) bool, error) {
	if n, _ := p.peekAt(1); n != ':' {
		return nil, refuse("preg pattern with a POSIX collating element")
	}
	p.pos += 2
	negate := false
	if p.more() && p.peek() == '^' {
		negate = true
		p.pos++
	}
	start := p.pos
	for p.more() && p.peek() != ':' {
		p.pos++
	}
	name := string(p.src[start:p.pos])
	if n, ok := p.peekAt(1); !ok || p.peek() != ':' || n != ']' {
		return nil, refuse("preg pattern with a malformed POSIX class")
	}
	p.pos += 2
	var pred func(rune) bool
	if p.utf {
		switch name {
		case "alpha":
			pred = unicode.IsLetter
		case "digit":
			pred = func(r rune) bool { return unicode.Is(unicode.Nd, r) }
		case "alnum":
			pred = func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }
		case "lower":
			pred = func(r rune) bool { return unicode.Is(unicode.Ll, r) }
		case "upper":
			pred = func(r rune) bool { return unicode.Is(unicode.Lu, r) }
		case "space":
			pred = func(r rune) bool { return unicode.Is(unicode.Z, r) || isHSpace(r) || isVSpace(r) }
		case "word":
			pred = isWordUCP
		case "blank":
			pred = isHSpace
		case "xdigit":
			pred = isHexDigit
		default:
			return nil, refuse("preg pattern with [:%s:] under /u", name)
		}
	} else {
		switch name {
		case "alpha":
			pred = func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }
		case "digit":
			pred = isDigitASCII
		case "alnum":
			pred = func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || isDigitASCII(r) }
		case "lower":
			pred = func(r rune) bool { return r >= 'a' && r <= 'z' }
		case "upper":
			pred = func(r rune) bool { return r >= 'A' && r <= 'Z' }
		case "space":
			pred = isSpaceASCII
		case "word":
			pred = isWordASCII
		case "blank":
			pred = func(r rune) bool { return r == ' ' || r == '\t' }
		case "xdigit":
			pred = isHexDigit
		case "cntrl":
			pred = func(r rune) bool { return r < 32 || r == 127 }
		case "punct":
			pred = func(r rune) bool { return r > 32 && r < 127 && !isWordASCII(r) || r == '_' }
		case "graph":
			pred = func(r rune) bool { return r > 32 && r < 127 }
		case "print":
			pred = func(r rune) bool { return r >= 32 && r < 127 }
		case "ascii":
			pred = func(r rune) bool { return r < 128 }
		default:
			return nil, refuse("preg pattern PCRE would not compile (unknown POSIX class name)")
		}
	}
	if negate {
		return func(r rune) bool { return !pred(r) }, nil
	}
	return pred, nil
}

// checkLookbehinds refuses a lookbehind whose branches are not each of one
// fixed length: that is the construct this matcher implements (it steps back
// the branch's length and matches forward to the current position).
func checkLookbehinds(n *reNode, utf bool) error {
	if n == nil {
		return nil
	}
	if n.op == reLook && n.behind {
		for _, b := range lookBranches(n) {
			if _, ok := fixedLength(b); !ok {
				return refuse("preg pattern with a variable-length lookbehind")
			}
		}
	}
	for _, s := range n.subs {
		if err := checkLookbehinds(s, utf); err != nil {
			return err
		}
	}
	return nil
}

func lookBranches(n *reNode) []*reNode {
	if n.subs[0].op == reAlt {
		return n.subs[0].subs
	}
	return n.subs
}

// fixedLength is a node's length in characters, when it has only one.
func fixedLength(n *reNode) (int, bool) {
	switch n.op {
	case reChar, reAny, reSet:
		return 1, true
	case reCat:
		total := 0
		for _, s := range n.subs {
			l, ok := fixedLength(s)
			if !ok {
				return 0, false
			}
			total += l
		}
		return total, true
	case reAlt:
		want := -1
		for _, s := range n.subs {
			l, ok := fixedLength(s)
			if !ok || (want >= 0 && l != want) {
				return 0, false
			}
			want = l
		}
		return want, want >= 0
	case reGroup, reAtomic:
		return fixedLength(n.subs[0])
	case reRepeat:
		if n.min != n.max {
			return 0, false
		}
		l, ok := fixedLength(n.subs[0])
		return l * n.min, ok
	case reLook, reBOL, reEOL, reStartSubject, reEndSubject, reEndSubjectNL, reWordBoundary, reNotWordBoundary, reStartOffset:
		return 0, true
	}
	return 0, false
}
