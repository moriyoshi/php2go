package phpt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/internal/phpt"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "case.phpt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseSections(t *testing.T) {
	test, err := phpt.Parse(write(t, `--TEST--
A descriptive name
--FILE--
<?php echo "hi";
--EXPECT--
hi
`))
	if err != nil {
		t.Fatal(err)
	}
	if test.Name != "A descriptive name" {
		t.Errorf("name = %q", test.Name)
	}
	if !strings.Contains(test.File, `echo "hi"`) {
		t.Errorf("file = %q", test.File)
	}
	if !test.Matches("hi") {
		t.Error("exact output should match")
	}
	if test.Matches("bye") {
		t.Error("different output must not match")
	}
	// Trailing whitespace is not significant, as in run-tests.php.
	if !test.Matches("hi\n\n") {
		t.Error("trailing newlines should be ignored")
	}
}

// EXPECTF placeholders stand in for output that varies per run — paths, line
// numbers, object ids. Treating them literally would fail every such test.
func TestExpectFPlaceholders(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		actual  string
		want    bool
	}{
		{"int(%d)", "int(42)", true},
		{"int(%d)", "int(abc)", false},
		{"in %s on line %d", "in /tmp/x.php on line 7", true},
		{"%a", "anything\nacross lines", true},
		{"%s", "one line only", true},
		{"%s", "two\nlines", false},
		{"value: %f", "value: -1.5e3", true},
		{"%x", "1a2b3c", true},
		// run-tests.php has no %% escape: it quotes the pattern and then
		// substitutes the specifiers it knows, so a doubled percent stays
		// doubled. Being more lenient here would pass tests the engine's own
		// harness fails.
		{"100%% done", "100% done", false},
		{"100%% done", "100%% done", true},
	} {
		test, err := phpt.Parse(write(t, "--TEST--\nt\n--FILE--\n<?php\n--EXPECTF--\n"+tc.pattern+"\n"))
		if err != nil {
			t.Fatal(err)
		}
		if got := test.Matches(tc.actual); got != tc.want {
			t.Errorf("pattern %q against %q = %t, want %t", tc.pattern, tc.actual, got, tc.want)
		}
	}
}

// A regex metacharacter in expected output is literal text, not a pattern.
func TestExpectFQuotesLiterals(t *testing.T) {
	test, err := phpt.Parse(write(t, "--TEST--\nt\n--FILE--\n<?php\n--EXPECTF--\narray(1) { [0]=> int(%d) }\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !test.Matches("array(1) { [0]=> int(5) }") {
		t.Error("parentheses and brackets should be literal")
	}
	if test.Matches("arrayX1Y { [0]=> int(5) }") {
		t.Error("literals must not behave as wildcards")
	}
}

// Tests needing machinery the interpreter lacks are skipped with a specific
// reason, so the metric never silently absorbs them.
func TestSkipReasons(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		// A --SKIPIF-- body is no longer a skip reason on its own: the runner
		// evaluates it, because whether it applies is a question about this
		// machine and this runtime rather than about the section's presence.
		{"--TEST--\nt\n--FILE--\n<?php\n--SKIPIF--\n<?php die('skip');\n--EXPECT--\nx\n", ""},
		{"--TEST--\nt\n--FILE--\n<?php\n--EXTENSIONS--\ncurl\n--EXPECT--\nx\n", "extensions"},
		{"--TEST--\nt\n--FILE--\n<?php\n--INI--\nprecision=14\n--EXPECT--\nx\n", "--INI--"},
		// A setting php2go can disregard is not a reason to skip.
		{"--TEST--\nt\n--FILE--\n<?php\n--INI--\nmemory_limit=256M\n--EXPECT--\nx\n", ""},
		// Lowering the limit is a test about running out of memory.
		{"--TEST--\nt\n--FILE--\n<?php\n--INI--\nmemory_limit=2M\n--EXPECT--\nx\n", "memory_limit"},
		{"--TEST--\nt\n--FILE--\n<?php echo 1;\n--EXPECT--\n1\n", ""},
	} {
		test, err := phpt.Parse(write(t, tc.body))
		if err != nil {
			t.Fatal(err)
		}
		reason := test.SkipReason()
		if tc.want == "" {
			if reason != "" {
				t.Errorf("runnable test was skipped: %s", reason)
			}
			continue
		}
		if !strings.Contains(reason, tc.want) {
			t.Errorf("skip reason = %q, want it to mention %q", reason, tc.want)
		}
	}
}

// A dashed line inside the PHP body is not a section header.
func TestBodyDashesAreNotSections(t *testing.T) {
	test, err := phpt.Parse(write(t, "--TEST--\nt\n--FILE--\n<?php echo \"--not a section--\";\n--EXPECT--\n--not a section--\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(test.File, "not a section") {
		t.Errorf("body was truncated at a dashed line: %q", test.File)
	}
}

func TestMissingFileSectionIsAnError(t *testing.T) {
	if _, err := phpt.Parse(write(t, "--TEST--\nno body\n--EXPECT--\nx\n")); err == nil {
		t.Fatal("a test with no --FILE-- should be rejected")
	}
}

// An EXPECTF pattern is built from the expectation's *bytes*. Reading each byte
// as a rune turns every byte of a multi-byte character into U+FFFD, so a
// pattern written from text containing one stops matching that very text.
func TestExpectFKeepsMultiByteCharacters(t *testing.T) {
	test, err := phpt.Parse(write(t,
		"--TEST--\nt\n--FILE--\n<?php echo \"x\";\n--EXPECTF--\nvalue:string(4) \"\U0001F418\" on line %d\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !test.Matches("value:string(4) \"\U0001F418\" on line 12") {
		t.Error("an expectation containing an emoji did not match itself")
	}
	if test.Matches("value:string(4) \"\U0001F419\" on line 12") {
		t.Error("a different character matched, so the pattern is not comparing bytes")
	}
}
