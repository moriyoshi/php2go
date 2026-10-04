package php_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

// engineOutput is what the real PHP engine printed for the probe below, captured
// by running the equivalent script through the embed SAPI. It is ground truth,
// not an expectation written from memory — the difference already mattered once:
// PHP skips leading whitespace when reading a number, so (int)"  8" is 8, and
// this probe is what caught the runtime returning 0.
//
// To re-derive, run this PHP through any php2go-built engine:
//
//	$b = ["", "0", "0.0", "false", " ", "abc"];
//	foreach ($b as $s) { echo "bool(", var_export((bool)$s, true), ") "; }  echo "\n";
//	$i = ["12","12abc","abc","","-7","3.9","1e3","  8"];
//	foreach ($i as $s) { echo (int)$s, " "; }  echo "\n";
//	$n = ["12","  12  ","12abc","1.5e3",".5","","-","0x1A"];
//	foreach ($n as $s) { echo is_numeric($s) ? "T" : "F"; }  echo "\n";
//	echo gettype(PHP_INT_MAX + 1), " ", gettype(PHP_INT_MAX * 2), " ", gettype(1 + 2), "\n";
//	echo gettype(6/3), " ", 6/3, " ", gettype(7/2), " ", 7/2, "\n";
//	... and so on, matching the sections below.
const engineOutput = `bool(false) bool(false) bool(true) bool(true) bool(true) bool(true) 
12 12 0 0 -7 3 1000 8 
TTFTTFFF
double double integer
integer 2 double 3.5
FTTTTTTTTT
1 1.5 -0.5 1.0E+15
`

func TestMatchesEngineSemantics(t *testing.T) {
	var b strings.Builder

	// Truthiness.
	for _, s := range []string{"", "0", "0.0", "false", " ", "abc"} {
		fmt.Fprintf(&b, "bool(%v) ", php.String(s).Bool())
	}
	b.WriteString("\n")

	// Integer coercion, including the leading-whitespace case.
	for _, s := range []string{"12", "12abc", "abc", "", "-7", "3.9", "1e3", "  8"} {
		fmt.Fprintf(&b, "%d ", php.String(s).Int())
	}
	b.WriteString("\n")

	// is_numeric, which is stricter than coercion.
	for _, s := range []string{"12", "  12  ", "12abc", "1.5e3", ".5", "", "-", "0x1A"} {
		if _, ok := php.String(s).IsNumericString(); ok {
			b.WriteString("T")
		} else {
			b.WriteString("F")
		}
	}
	b.WriteString("\n")

	// Overflow promotes to float instead of wrapping.
	const maxInt = int64(^uint64(0) >> 1)
	fmt.Fprintf(&b, "%s %s %s\n",
		engineType(php.Add(php.Int(maxInt), php.Int(1))),
		engineType(php.Mul(php.Int(maxInt), php.Int(2))),
		engineType(php.Add(php.Int(1), php.Int(2))))

	// Division is integral only when exact.
	sixOverThree, _ := php.Div(php.Int(6), php.Int(3))
	sevenOverTwo, _ := php.Div(php.Int(7), php.Int(2))
	fmt.Fprintf(&b, "%s %s %s %s\n",
		engineType(sixOverThree), sixOverThree.String(),
		engineType(sevenOverTwo), sevenOverTwo.String())

	// Loose equality under PHP 8 string semantics.
	for _, pair := range [][2]php.Value{
		{php.Int(0), php.String("abc")},
		{php.Int(0), php.String("0")},
		{php.Int(1), php.String("1")},
		{php.Int(1), php.String(" 1")},
		{php.String("1"), php.String("01")},
		{php.String("10"), php.String("1e1")},
		{php.String("abc"), php.String("abc")},
		{php.Null(), php.String("")},
		{php.Bool(true), php.Int(42)},
		{php.Bool(false), php.String("")},
	} {
		if php.LooseEqual(pair[0], pair[1]) {
			b.WriteString("T")
		} else {
			b.WriteString("F")
		}
	}
	b.WriteString("\n")

	// Float rendering, including PHP's 1.0E+15 spelling.
	fmt.Fprintf(&b, "%s %s %s %s\n",
		php.Float(1).String(), php.Float(1.5).String(),
		php.Float(-0.5).String(), php.Float(1e15).String())

	if got := b.String(); got != engineOutput {
		t.Errorf("runtime diverges from the engine.\n--- engine ---\n%s\n--- runtime ---\n%s", engineOutput, got)
	}
}

// engineType renders a value's type as PHP's gettype() spells it.
func engineType(v php.Value) string {
	switch v.Kind() {
	case php.KindInt:
		return "integer"
	case php.KindFloat:
		return "double"
	}
	return v.Kind().String()
}
