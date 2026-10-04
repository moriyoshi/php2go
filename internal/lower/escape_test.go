package lower_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moriyoshi/php2go/internal/ir"
	"github.com/moriyoshi/php2go/internal/lower"
	"github.com/moriyoshi/php2go/internal/zend"
)

// lowerOne compiles a snippet and returns the body of its function `t`. The
// snippet goes through a temporary file because the engine compiles files.
func lowerOne(t *testing.T, src string) *lower.Function {
	t.Helper()
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
	for _, oa := range ir.EncodeUnit(unit).GetFunctions() {
		if oa.GetName() != "t" {
			continue
		}
		fn, err := lw.Lower(oa)
		if err != nil {
			t.Fatal(err)
		}
		return fn
	}
	t.Fatal("function t not lowered")
	return nil
}

// The cases are written to **break** the claim rather than to confirm it: the
// analysis is only safe if every way an object can outlive its frame leaves it
// unclaimed, so what matters is that the escaping shapes are refused.
func TestConfinedLocalsRefusesEveryEscape(t *testing.T) {
	never := func(string) bool { return false }
	for _, c := range []struct {
		name    string
		src     string
		claimed bool
	}{
		{"plain local", `function t() { $a = new D(); }`, true},
		{"property read", `function t() { $a = new D(); $x = $a->p; }`, true},
		{"property write on it", `function t() { $a = new D(); $a->p = 1; }`, true},
		{"returned", `function t() { $a = new D(); return $a; }`, false},
		{"passed to a function", `function t() { $a = new D(); f($a); }`, false},
		{"stored in an array", `function t() { $a = new D(); $arr[] = $a; }`, false},
		{"stored in a property", `function t($o) { $a = new D(); $o->p = $a; }`, false},
		{"bound by reference", `function t() { $a = new D(); $r = &$a; }`, false},
		{"captured by a closure", `function t() { $a = new D(); $f = function () use ($a) {}; }`, false},
		// A copy that is never read vanishes in lowering and the object is
		// genuinely confined, so the case has to make the copy observable.
		{"copied and returned", `function t() { $a = new D(); $b = $a; return $b; }`, false},
		{"copied and unset", `function t() { $a = new D(); $b = $a; unset($a); return $b; }`, false},
		{"yielded", `function t() { $a = new D(); yield $a; }`, false},
		// The argument becomes the contents of the constructed object, which is
		// returned; only the receiver of a constructor call is the frame's.
		{"passed to a constructor", `class H { function __construct($x) { $this->x = $x; } }
			function t() { return new H(new D()); }`, false},
		{"body uses extract", `function t($z) { $a = new D(); extract($z); }`, false},
		{"body uses a variable variable", `function t($n) { $a = new D(); $$n = 1; }`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php class D {} "+c.src)
			got := len(lower.ConfinedObjects(fn, never)) > 0
			if got != c.claimed {
				t.Fatalf("claimed = %v, want %v\n%s", got, c.claimed, fn)
			}
		})
	}
}

// A method call hands the object over as `$this`, so it is claimable only when
// the method is known not to store it -- which is what the summary supplies.
func TestConfinedLocalsUsesTheMethodSummary(t *testing.T) {
	src := `<?php class D { function m() {} } function t() { $a = new D(); $a->m(); }`
	fn := lowerOne(t, src)
	if n := len(lower.ConfinedObjects(fn, nil)); n != 0 {
		t.Fatalf("with no summary the receiver must escape, claimed %d", n)
	}
	if n := len(lower.ConfinedObjects(fn, func(string) bool { return false })); n != 1 {
		t.Fatalf("with a summary saying no method leaks $this, want 1 claim, got %d", n)
	}
	if n := len(lower.ConfinedObjects(fn, func(string) bool { return true })); n != 0 {
		t.Fatalf("a method that leaks $this must not be claimed, got %d", n)
	}
}
