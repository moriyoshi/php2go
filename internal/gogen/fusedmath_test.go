package gogen_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/moriyoshi/php2go/internal/gogen"
	"github.com/moriyoshi/php2go/internal/ir"
	"github.com/moriyoshi/php2go/internal/lower"
	"github.com/moriyoshi/php2go/internal/zend"
)

// Native float arithmetic is converted to float64 at every result unless the
// program asks for fused maths, which emits the expressions verbatim so the Go
// compiler may turn `a*b + c` into one multiply-add. The default is what keeps
// results bit-identical with PHP; see gogen.Program.FusedMath.
func TestFusedMathEmitsFloatArithmeticVerbatim(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fma.php")
	src := "<?php\nfunction fma(float $a, float $b, float $c) { return $a * $b + $c; }\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	frameless, _ := engine.FramelessFunctions()
	emit := func(fused bool) string {
		t.Helper()
		lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
		encoded := ir.EncodeUnit(unit)
		var fns []*lower.Function
		for _, oa := range encoded.GetFunctions() {
			fn, err := lw.Lower(oa)
			if err != nil {
				t.Fatal(err)
			}
			lower.Specialize(fn, nil)
			if lower.FloatReps(fn, nil) == 0 {
				t.Fatalf("no value of %s was carried as a float64", fn.Name)
			}
			fns = append(fns, fn)
		}
		out, err := gogen.Emit(&gogen.Program{Functions: fns, FusedMath: fused})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	strict, fused := emit(false), emit(true)
	// The snippet has no literals, so `vN = float64(` can only be a rounded
	// result, never an operand's conversion.
	rounded := regexp.MustCompile(`(?m)^\s*v\d+ = float64\(`)
	native := regexp.MustCompile(`(?m)^\s*v\d+ = .* [*+] `)
	if got := len(rounded.FindAllString(strict, -1)); got != 2 {
		t.Errorf("default emission rounds %d results, want the multiply and the add:\n%s", got, strict)
	}
	if rounded.MatchString(fused) {
		t.Errorf("fused emission still rounds a result:\n%s", fused)
	}
	if got := len(native.FindAllString(fused, -1)); got != 2 {
		t.Errorf("fused emission has %d native operations, want 2:\n%s", got, fused)
	}
}
