package lower_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moriyoshi/php2go/internal/ir"
	"github.com/moriyoshi/php2go/internal/lower"
	"github.com/moriyoshi/php2go/internal/zend"
)

// Each case is an attempt to get a `false` out of LeaksThis for a method that
// really does let the receiver out. A false is a promise the handle machinery
// acts on, so a wrong one frees a live object; a wrong `true` only costs a
// missed claim.
func TestLeaksThis(t *testing.T) {
	cases := []struct {
		name  string
		php   string
		leaks bool
	}{
		{"writes own property", `$this->x = 1;`, false},
		{"reads own property", `return $this->x;`, false},
		{"nested write", `$this->arr[] = 1;`, false},
		{"isset on own property", `return isset($this->x);`, false},
		{"unset own property", `unset($this->x);`, false},
		{"no mention at all", `return 1;`, false},

		{"stored in a static", `self::$s = $this;`, true},
		{"stored in a global", `global $k; $k = $this;`, true},
		{"returned", `return $this;`, true},
		{"into an array", `$a = [$this];`, true},
		{"passed to a function", `f($this);`, true},
		{"captured by a closure", `$c = function() { return $this; };`, true},
		{"method call on self", `$this->m();`, true},
		{"stored in another object", `$o = new stdClass; $o->p = $this;`, true},
		{"written into own property", `$this->x = $this;`, true},
		{"assigned to a local", `$v = $this;`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := methodBody(t, c.php)
			if got := lower.LeaksThis(fn); got != c.leaks {
				t.Fatalf("LeaksThis = %v, want %v\nbody:\n%s", got, c.leaks, fn)
			}
		})
	}
}

// methodBody lowers `class C { ... function probe() { <php> } }` and returns
// the probe's body. Separate from lowerOne because the subject is a **method**,
// which is reached through the class table rather than the function list.
func methodBody(t *testing.T, body string) *lower.Function {
	t.Helper()
	src := "<?php\nfunction f($a) {}\n" +
		"class C { public $x; public $arr = []; public static $s;\n" +
		"function m() {}\n" +
		"function probe() { " + body + " } }\n"
	path := filepath.Join(t.TempDir(), "case.php")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	frameless, err := engine.FramelessFunctions()
	if err != nil {
		t.Fatal(err)
	}
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	for _, c := range ir.EncodeUnit(unit).GetClasses() {
		for _, m := range c.GetMethods() {
			if m.GetName() != "probe" {
				continue
			}
			fn, err := lw.Lower(m)
			if err != nil {
				t.Fatal(err)
			}
			return fn
		}
	}
	t.Fatalf("no probe body in:\n%s", src)
	return nil
}
