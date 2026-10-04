// Package phpt reads php-src's own .phpt tests.
//
// These are the honest completeness metric for the interpreter: roughly twenty
// thousand tests written by the people who wrote the engine, with expected
// output they maintain. Grading against them measures what actually works
// rather than what was remembered to be tested.
package phpt

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Test is a parsed .phpt file.
type Test struct {
	Path string
	Name string
	// File is the PHP source under test.
	File string
	// Expect is literal expected output; ExpectF is a pattern. Exactly one is
	// normally present.
	Expect  string
	ExpectF string
	// ExpectRegex is a raw regular expression, used by a handful of tests.
	ExpectRegex string

	// Sections whose presence means the test needs machinery the interpreter
	// does not have yet. Recorded rather than ignored so the skip reason is
	// specific.
	SkipIf     string
	Extensions []string
	Ini        string
	HasClean   bool
}

// Parse reads a .phpt file.
func Parse(path string) (*Test, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	test := &Test{Path: path}

	// Sections are introduced by a --NAME-- line; everything until the next
	// such line belongs to the current section.
	var section string
	var body strings.Builder
	flush := func() {
		if section == "" {
			return
		}
		text := body.String()
		switch section {
		case "TEST":
			test.Name = strings.TrimSpace(text)
		case "FILE":
			test.File = text
		case "EXPECT":
			test.Expect = text
		case "EXPECTF":
			test.ExpectF = text
		case "EXPECTREGEX":
			test.ExpectRegex = text
		case "SKIPIF":
			test.SkipIf = text
		case "INI":
			test.Ini = text
		case "EXTENSIONS":
			for _, line := range strings.Split(text, "\n") {
				if name := strings.TrimSpace(line); name != "" {
					test.Extensions = append(test.Extensions, name)
				}
			}
		case "CLEAN":
			test.HasClean = true
		}
		body.Reset()
	}

	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, "--") && strings.HasSuffix(trimmed, "--") && len(trimmed) > 4 {
			name := strings.TrimSuffix(strings.TrimPrefix(trimmed, "--"), "--")
			if isSectionName(name) {
				flush()
				section = name
				continue
			}
		}
		if section != "" {
			body.WriteString(trimmed)
			body.WriteString("\n")
		}
	}
	flush()

	if test.File == "" {
		return nil, fmt.Errorf("%s: no --FILE-- section", path)
	}
	return test, nil
}

// isSectionName keeps a line like "--- foo ---" inside a test body from being
// mistaken for a section header.
func isSectionName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c < 'A' || c > 'Z') && c != '_' {
			return false
		}
	}
	return true
}

// phpTrimCutset is PHP's default trim() character list. run-tests.php trims
// both the output and the expectation with it, so a leading blank line -- which
// is exactly what the engine emits before a fatal error -- is not significant.
const phpTrimCutset = " \t\n\r\x00\x0B"

// Matches reports whether actual output satisfies the test's expectation.
func (t *Test) Matches(actual string) bool {
	actual = normalizeNewlines(strings.Trim(actual, phpTrimCutset))

	switch {
	case t.Expect != "":
		return actual == normalizeNewlines(strings.Trim(t.Expect, phpTrimCutset))
	case t.ExpectF != "":
		pattern := expectFToRegexp(normalizeNewlines(strings.Trim(t.ExpectF, phpTrimCutset)))
		re, err := regexp.Compile("(?s)\\A" + pattern + "\\z")
		if err != nil {
			return false
		}
		return re.MatchString(actual)
	case t.ExpectRegex != "":
		re, err := regexp.Compile("(?s)\\A" + normalizeNewlines(strings.Trim(t.ExpectRegex, phpTrimCutset)) + "\\z")
		if err != nil {
			return false
		}
		return re.MatchString(actual)
	}
	return actual == ""
}

// normalizeNewlines collapses CRLF to LF on both sides of the comparison, which
// is what run-tests.php does before comparing. A heredoc whose body carries
// CRLF otherwise fails against an expectation written with LF, and the two are
// indistinguishable in a printed diff.
func normalizeNewlines(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

// expectFToRegexp translates the %-placeholders run-tests.php understands.
func expectFToRegexp(format string) string {
	var out strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			// One *byte*, kept as a byte: `string(format[i])` would read it as
			// a rune, so every byte of a multi-byte character above U+007F
			// would become U+FFFD and the pattern would no longer match the
			// text it was written from. An expectation containing an emoji is
			// enough to see it.
			out.WriteString(regexp.QuoteMeta(format[i : i+1]))
			continue
		}
		i++
		switch format[i] {
		case 'e':
			out.WriteString(regexp.QuoteMeta(string(os.PathSeparator)))
		case 's':
			out.WriteString(`[^\r\n]+`)
		case 'S':
			out.WriteString(`[^\r\n]*`)
		case 'a':
			out.WriteString(`.+`)
		case 'A':
			out.WriteString(`.*`)
		case 'w':
			out.WriteString(`\s*`)
		case 'i':
			out.WriteString(`[+-]?\d+`)
		case 'd':
			out.WriteString(`\d+`)
		case 'x':
			out.WriteString(`[0-9a-fA-F]+`)
		case 'f':
			// run-tests.php writes this with a lookahead, which RE2 does not
			// have. The alternation accepts the same language: digits with an
			// optional fraction, or a fraction with no leading digits, either
			// way with an optional exponent.
			out.WriteString(`[+-]?(?:\d+(?:\.\d+)?|\.\d+)(?:[Ee][+-]?\d+)?`)
		case 'c':
			out.WriteString(`.`)
		case '0':
			// A NUL byte, which cannot be written into a .phpt file directly.
			// run-tests.php spells it this way and so do the tests that mangle
			// private property names.
			out.WriteString(`\x00`)
		default:
			out.WriteString(regexp.QuoteMeta("%" + string(format[i])))
		}
	}
	return out.String()
}

// ignorableIni are the settings that cannot change what php2go does. Some are
// about a subsystem it does not have (opcache, sessions), some about limits it
// does not impose (memory, stack, time), and some about how the *engine*
// reports errors rather than what a program observes.
var ignorableIni = map[string]bool{
	"max_execution_time": true, "date.timezone": true,
	"report_memleaks": true, "log_errors": true, "html_errors": true,
	"docref_root": true, "docref_ext": true, "error_log": true,
	"session.save_handler": true, "session.save_path": true,
	"error_reporting": true, "display_errors": true, "display_startup_errors": true,
	// php2go has one collector and it is the only thing that destroys an
	// object, so switching *cycle* collection off would switch off ordinary
	// destruction with it. Disregarding the setting keeps destructors running
	// where PHP's refcount would have run them.
	"zend.enable_gc": true,
}

// UnhonourableIni names the first --INI-- setting that would change what the
// program does and that php2go can neither apply nor disregard, or "" when the
// test can be run as written.
func (t *Test) UnhonourableIni() string {
	for _, line := range strings.Split(t.Ini, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		name := strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "opcache.") || strings.HasPrefix(lower, "session.") {
			continue
		}
		if lower == "memory_limit" {
			// Raising the limit, or removing it, only lets a test allocate
			// what php2go would have let it allocate anyway. *Lowering* it is
			// a test about running out of memory, which php2go cannot run out
			// of -- it would run out of the machine's instead.
			if value := strings.TrimSpace(strings.SplitN(line, "=", 2)[1]); !raisesMemoryLimit(value) {
				return name + "=" + value
			}
			continue
		}
		if ignorableIni[lower] {
			continue
		}
		return name
	}
	return ""
}

// raisesMemoryLimit reports whether a memory_limit setting leaves at least as
// much room as the default 128M, which is the case php2go can disregard.
func raisesMemoryLimit(value string) bool {
	value = strings.TrimSpace(value)
	if value == "-1" {
		return true
	}
	multiplier := int64(1)
	switch {
	case strings.HasSuffix(strings.ToUpper(value), "K"):
		multiplier, value = 1<<10, value[:len(value)-1]
	case strings.HasSuffix(strings.ToUpper(value), "M"):
		multiplier, value = 1<<20, value[:len(value)-1]
	case strings.HasSuffix(strings.ToUpper(value), "G"):
		multiplier, value = 1<<30, value[:len(value)-1]
	}
	bytes, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return false
	}
	return bytes*multiplier >= 128<<20
}

// IniValue reads one setting from the test's --INI-- section.
func (t *Test) IniValue(name string) (string, bool) {
	for _, line := range strings.Split(t.Ini, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), name) {
			return strings.TrimSpace(parts[1]), true
		}
	}
	return "", false
}

// SkipReason explains why a test cannot be attempted yet, or "" if it can.
func (t *Test) SkipReason() string {
	if len(t.Extensions) > 0 {
		return "needs extensions: " + strings.Join(t.Extensions, ", ")
	}
	if setting := t.UnhonourableIni(); setting != "" {
		// A setting that changes what the program does and that php2go cannot
		// apply would make the comparison meaningless: the two sides would be
		// running different configurations.
		return "has --INI-- " + setting
	}
	if t.HasClean {
		return "has --CLEAN--"
	}
	if t.Expect == "" && t.ExpectF == "" && t.ExpectRegex == "" {
		return "no expectation section"
	}
	return ""
}
