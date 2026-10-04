package php_test

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"unsafe"

	"github.com/moriyoshi/php2go/runtime/php"
)

// PHP's truthiness is not Go's, and the exceptions are the whole point: "0" is
// false but "0.0" and "false" are true.
func TestBool(t *testing.T) {
	for _, tc := range []struct {
		value php.Value
		want  bool
	}{
		{php.Undef(), false},
		{php.Null(), false},
		{php.Int(0), false},
		{php.Int(-1), true},
		{php.Float(0), false},
		{php.String(""), false},
		{php.String("0"), false},
		{php.String("0.0"), true},
		{php.String("false"), true},
		{php.String(" "), true},
		{php.Arr(php.NewArray()), false},
	} {
		if got := tc.value.Bool(); got != tc.want {
			t.Errorf("%s(%q).Bool() = %t, want %t", tc.value.Kind(), tc.value.String(), got, tc.want)
		}
	}
}

// Int takes a leading numeric prefix and ignores the rest.
func TestIntCoercion(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"12", 12},
		{"12abc", 12},
		{"abc", 0},
		{"", 0},
		{"-7", -7},
		{"3.9", 3},
		{"1e3", 1000},
		{"  8", 8}, // PHP skips leading whitespace
	} {
		if got := php.String(tc.in).Int(); got != tc.want {
			t.Errorf("String(%q).Int() = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// Only a fully numeric string is numeric; a prefix is not enough.
func TestIsNumericString(t *testing.T) {
	for _, tc := range []struct {
		in      string
		numeric bool
	}{
		{"12", true},
		{"  12  ", true},
		{"12abc", false},
		{"1.5e3", true},
		{".5", true},
		{"", false},
		{"-", false},
		{"0x1A", false}, // hex strings stopped being numeric in PHP 7
	} {
		if _, ok := php.String(tc.in).IsNumericString(); ok != tc.numeric {
			t.Errorf("IsNumericString(%q) = %t, want %t", tc.in, ok, tc.numeric)
		}
	}
}

// Integer overflow promotes to float rather than wrapping as Go would.
func TestArithmeticOverflowPromotes(t *testing.T) {
	const maxInt = int64(^uint64(0) >> 1)
	sum := php.Add(php.Int(maxInt), php.Int(1))
	if sum.Kind() != php.KindFloat {
		t.Errorf("overflowing add = %s, want float", sum.Kind())
	}
	product := php.Mul(php.Int(maxInt), php.Int(2))
	if product.Kind() != php.KindFloat {
		t.Errorf("overflowing multiply = %s, want float", product.Kind())
	}
	if ok := php.Add(php.Int(1), php.Int(2)); ok.Kind() != php.KindInt || ok.Int() != 3 {
		t.Errorf("1 + 2 = %s %d", ok.Kind(), ok.Int())
	}
}

// Division stays integral only when exact.
func TestDiv(t *testing.T) {
	q, ok := php.Div(php.Int(6), php.Int(3))
	if !ok || q.Kind() != php.KindInt || q.Int() != 2 {
		t.Errorf("6/3 = %s %v (ok=%t)", q.Kind(), q.Int(), ok)
	}
	q, ok = php.Div(php.Int(7), php.Int(2))
	if !ok || q.Kind() != php.KindFloat || q.Float() != 3.5 {
		t.Errorf("7/2 = %s %v", q.Kind(), q.Float())
	}
	if _, ok := php.Div(php.Int(1), php.Int(0)); ok {
		t.Error("division by zero should be reported, not silently produce a value")
	}
}

// PHP 8 changed this: a number compared with a non-numeric string now compares
// as strings instead of coercing the string to 0. Getting it wrong would make
// 0 == "abc" true, as it was in PHP 7.
func TestLooseEqualPHP8StringSemantics(t *testing.T) {
	for _, tc := range []struct {
		a, b php.Value
		want bool
	}{
		{php.Int(0), php.String("abc"), false},
		{php.Int(0), php.String("0"), true},
		{php.Int(1), php.String("1"), true},
		{php.Int(1), php.String(" 1"), true},
		{php.String("1"), php.String("01"), true},
		{php.String("10"), php.String("1e1"), true},
		{php.String("abc"), php.String("abc"), true},
		{php.Null(), php.String(""), true},
		{php.Bool(true), php.Int(42), true},
		{php.Bool(false), php.String(""), true},
	} {
		if got := php.LooseEqual(tc.a, tc.b); got != tc.want {
			t.Errorf("%q == %q = %t, want %t", tc.a.String(), tc.b.String(), got, tc.want)
		}
	}
}

func TestStrictEqual(t *testing.T) {
	if php.StrictEqual(php.Int(1), php.String("1")) {
		t.Error("=== must not coerce across types")
	}
	if !php.StrictEqual(php.Int(1), php.Int(1)) {
		t.Error("1 === 1")
	}
	if !php.StrictEqual(php.Bool(false), php.Bool(false)) {
		t.Error("false === false")
	}
}

func TestFloatFormatting(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{1, "1"},
		{1.5, "1.5"},
		{-0.5, "-0.5"},
		{1e15, "1.0E+15"},
	} {
		if got := php.Float(tc.in).String(); got != tc.want {
			t.Errorf("Float(%v).String() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Converting an array to a string prints "Array" and raises a warning; only Ctx
// can record it, so Echo owns it. PHP 8 raises this at E_WARNING -- it was a
// notice in PHP 7, and the engine settled which one applies here.
//
// PHP writes a diagnostic into the script's own output, so it appears there as
// well as in the recorded list -- a test comparing expected output is comparing
// against text that includes it.
func TestEchoArrayRaisesWarning(t *testing.T) {
	var out bytes.Buffer
	ctx := php.NewCtx(&out)
	ctx.SetLocation("t.php", 3)
	ctx.Echo(php.Arr(php.NewArray()))

	const want = "\nWarning: Array to string conversion in t.php on line 3\nArray"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
	if len(ctx.Diagnostics()) != 1 || ctx.Diagnostics()[0].Level != "Warning" {
		t.Errorf("diagnostics = %+v, want one warning", ctx.Diagnostics())
	}
}

// A suppressed diagnostic reaches neither the output nor the list, which is what
// makes @ observable at all.
func TestSilencedDiagnosticIsNotPrinted(t *testing.T) {
	var out bytes.Buffer
	ctx := php.NewCtx(&out)
	ctx.BeginSilence()
	ctx.Echo(php.Arr(php.NewArray()))
	if out.String() != "Array" {
		t.Errorf("output = %q, want Array", out.String())
	}
	if len(ctx.Diagnostics()) != 0 {
		t.Errorf("diagnostics = %+v, want none", ctx.Diagnostics())
	}
}

// The array-key coercion table, every row read back from the engine by writing
// the value into an array and printing the key:
//
//	$a = []; $a[$v] = 1; foreach ($a as $key => $_) var_dump($key);
//
// Three rows are not guessable, and the compiled emitter had written its own
// copy of this before it moved here -- which is the divergence the shared
// surface exists to prevent.
func TestArrayKeyMatchesTheEngine(t *testing.T) {
	type want struct {
		isInt bool
		i     int64
		s     string
		diag  string // a fragment of the diagnostic, "" for silence
	}
	for _, c := range []struct {
		in   php.Value
		want want
	}{
		// true and false are integers, so $a[true] and $a[1] are one slot.
		{php.Bool(true), want{isInt: true, i: 1}},
		{php.Bool(false), want{isInt: true, i: 0}},
		// null is the only non-numeric coercion, and it is "" rather than 0.
		{php.Null(), want{s: ""}},
		{php.Int(5), want{isInt: true, i: 5}},
		{php.Int(-3), want{isInt: true, i: -3}},
		// A float truncates toward zero. The deprecation fires on *precision
		// loss*, not on being a float, so a whole float is silent.
		{php.Float(5.0), want{isInt: true, i: 5}},
		{php.Float(5.9), want{isInt: true, i: 5, diag: "Implicit conversion from float 5.9 to int loses precision"}},
		{php.Float(-5.9), want{isInt: true, i: -5, diag: "loses precision"}},
		// Canonical decimal strings become ints; everything else stays a string.
		{php.String("5"), want{isInt: true, i: 5}},
		{php.String("-3"), want{isInt: true, i: -3}},
		{php.String("0"), want{isInt: true, i: 0}},
		{php.String("05"), want{s: "05"}},
		{php.String("5.0"), want{s: "5.0"}},
		{php.String("1e3"), want{s: "1e3"}},
		{php.String(" 5"), want{s: " 5"}},
		{php.String("5 "), want{s: "5 "}},
		{php.String(""), want{s: ""}},
		{php.String("abc"), want{s: "abc"}},
		// The boundary: PHP_INT_MAX keys as an int, one past it stays a string.
		{php.String("9223372036854775807"), want{isInt: true, i: 9223372036854775807}},
		{php.String("9223372036854775808"), want{s: "9223372036854775808"}},
		// The rows a hand-written implementation gets wrong, and the reason
		// this table exists. The compiled emitter's copy did `int64(f)` on a
		// NaN, which is **undefined behaviour in Go** -- it gives whatever the
		// platform feels like, and passes any test written on the platform it
		// was written on.
		//
		// They also settle *why* the deprecation fires. It is about **precision
		// loss**, not about being a float, which is the only formulation that
		// predicts NAN and the infinities correctly: all three lose everything,
		// so all three deprecate and all three key as 0.
		{php.Float(math.NaN()), want{isInt: true, i: 0, diag: "from float NAN to int loses precision"}},
		{php.Float(math.Inf(1)), want{isInt: true, i: 0, diag: "from float INF to int loses precision"}},
		{php.Float(math.Inf(-1)), want{isInt: true, i: 0, diag: "from float -INF to int loses precision"}},
		// And out of range PHP **wraps** rather than saturating, so Go's own
		// cast cannot be the judge of whether precision was lost.
		{php.Float(1e19), want{isInt: true, i: -8446744073709551616, diag: "loses precision"}},
	} {
		var out strings.Builder
		ctx := php.NewCtx(&out)
		got := php.ArrayKey(ctx, c.in)
		if got.IsInt != c.want.isInt || (c.want.isInt && got.Int != c.want.i) ||
			(!c.want.isInt && got.Str != c.want.s) {
			t.Errorf("ArrayKey(%s %q) = %+v, want %+v", c.in.Kind(), c.in.String(), got, c.want)
		}
		switch diag := out.String(); {
		case c.want.diag == "" && diag != "":
			t.Errorf("ArrayKey(%s %q) said %q, want silence", c.in.Kind(), c.in.String(), diag)
		case c.want.diag != "" && !strings.Contains(diag, c.want.diag):
			t.Errorf("ArrayKey(%s %q) said %q, want it to contain %q",
				c.in.Kind(), c.in.String(), diag, c.want.diag)
		}
	}
}

// A Value is copied at every argument, return, array element and property, so
// its size is a cost everywhere. It is 24 bytes: a kind, one word of payload
// and one pointer. Growing it back is a decision, not an accident.
func TestValueIsThreeWords(t *testing.T) {
	if size := unsafe.Sizeof(php.Value{}); size != 24 {
		t.Fatalf("php.Value is %d bytes, want 24", size)
	}
}

// ToBool and NotBool take the two bool kinds on a fast path of their own and
// everything else through Value.Bool; both halves must agree with it.
func TestToBoolAndNotBoolAgreeWithBool(t *testing.T) {
	full := php.NewArray()
	full.Append(php.Int(1))
	for _, v := range []php.Value{
		php.Undef(), php.Null(), php.Bool(true), php.Bool(false), php.Int(0), php.Int(-3), php.Float(0), php.Float(math.Copysign(0, -1)),
		php.Float(math.NaN()), php.String(""), php.String("0"), php.String("0.0"), php.Arr(php.NewArray()), php.Arr(full),
	} {
		want := v.Bool()
		if got := php.ToBool(v); got.Kind() != php.Bool(want).Kind() {
			t.Errorf("php.ToBool(%v) = %v, want %v", v, got, want)
		}
		if got := php.NotBool(v); got.Kind() != php.Bool(!want).Kind() {
			t.Errorf("php.NotBool(%v) = %v, want %v", v, got, !want)
		}
	}
}
