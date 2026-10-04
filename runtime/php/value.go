// Package php implements PHP's value semantics for compiled and interpreted
// code alike.
//
// Nothing in this package holds mutable state at package scope. PHP is
// shared-nothing per request while a Go process is long-lived, so every
// superglobal, static, ini setting and open resource hangs off a *Ctx. A
// package-level variable would leak data between concurrent requests, which is
// both a correctness and a security failure, and an expensive one to retrofit.
package php

import (
	"math"
	"strconv"
	"strings"
	"unsafe"
)

// Kind is a PHP value type. The order matches the engine's IS_* constants so
// harvested literals map across without translation.
type Kind uint8

const (
	KindUndef Kind = iota
	KindNull
	KindFalse
	KindTrue
	KindInt
	KindFloat
	KindString
	KindArray
	KindObject
	// KindResource is a handle onto something the runtime owns. It keeps the
	// engine's IS_RESOURCE number so KindRef lines up with IS_REFERENCE.
	KindResource
	KindRef
)

func (k Kind) String() string {
	switch k {
	case KindUndef:
		return "undef"
	case KindNull:
		return "null"
	case KindFalse, KindTrue:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindArray:
		return "array"
	case KindObject:
		return "object"
	case KindResource:
		return "resource"
	case KindRef:
		return "reference"
	}
	return "unknown"
}

// Value is a PHP value.
//
// Booleans are two kinds rather than a field, as in the engine, so a bare Value
// is a valid false and the zero Value is undef — which is what an unset
// variable is, distinct from null.
//
// Strings are Go strings: PHP strings are immutable byte strings with
// copy-on-write, and a Go string gives the same observable behaviour. They are
// byte strings, not text, so no encoding is assumed anywhere.
type Value struct {
	// _ makes Value incomparable: with a string held as a pointer and a
	// length, `==` would compare where two strings live rather than what
	// they say, so no code may use it.
	_    [0]func()
	kind Kind
	// interned distinguishes compile-time strings from request-allocated ones.
	// Ordinary PHP semantics do not care, but debug_zval_dump exposes it.
	interned bool
	// num is the integer, the float's bits, or the string's length.
	num int64
	// ptr is the string's bytes, or the *Array, *Object, *Ref or *Resource.
	// One word for all of them keeps a Value at 24 bytes where a field per
	// kind made it 88 -- and a Value is copied at every argument, return,
	// array element and property.
	ptr unsafe.Pointer
}

// The payload, read through its kind: each answers the zero value for a
// Value of another kind, which is what reading its own field did when every
// kind had one.
func (v Value) s() string {
	if v.kind != KindString {
		return ""
	}
	return unsafe.String((*byte)(v.ptr), int(v.num))
}

func (v Value) a() *Array {
	if v.kind != KindArray {
		return nil
	}
	return (*Array)(v.ptr)
}

func (v Value) o() *Object {
	if v.kind != KindObject {
		return nil
	}
	return (*Object)(v.ptr)
}

func (v Value) r() *Ref {
	if v.kind != KindRef {
		return nil
	}
	return (*Ref)(v.ptr)
}

func (v Value) rs() *Resource {
	if v.kind != KindResource {
		return nil
	}
	return (*Resource)(v.ptr)
}

func (v Value) i() int64 {
	if v.kind != KindInt {
		return 0
	}
	return v.num
}

func (v Value) f() float64 {
	if v.kind != KindFloat {
		return 0
	}
	return math.Float64frombits(uint64(v.num))
}

// Constructors.

func Undef() Value      { return Value{} }
func Null() Value       { return Value{kind: KindNull} }
func Int(v int64) Value { return Value{kind: KindInt, num: v} }
func Float(v float64) Value {
	return Value{kind: KindFloat, num: int64(math.Float64bits(v))}
}
func String(v string) Value {
	return Value{kind: KindString, num: int64(len(v)), ptr: unsafe.Pointer(unsafe.StringData(v))}
}
func InternedString(v string) Value {
	s := String(v)
	s.interned = true
	return s
}
func Arr(v *Array) Value { return Value{kind: KindArray, ptr: unsafe.Pointer(v)} }

func Bool(v bool) Value {
	if v {
		return Value{kind: KindTrue}
	}
	return Value{kind: KindFalse}
}

// Accessors.

func (v Value) Kind() Kind        { return v.kind }
func (v Value) IsUndef() bool     { return v.kind == KindUndef }
func (v Value) Array() *Array     { return v.a() }
func (v Value) RawInt() int64     { return v.i() }
func (v Value) RawFloat() float64 { return v.f() }
func (v Value) RawString() string { return v.s() }
func (v Value) IsInterned() bool  { return v.interned }

// Copy returns the value an assignment produces. PHP arrays are value types:
// $b = $a gives $b an independent array, so sharing the pointer would make
// them alias -- and $a[0] = $a would build a genuine cycle rather than nesting
// a copy.
//
// The engine defers this with copy-on-write and a refcount. Go has no refcount
// to hang that on, so the copy is eager: correct first, cheaper later.
func (v Value) Copy() Value {
	// Assigning from a reference copies what it points at: `$b = $a` gives $b an
	// independent value even when $a is a reference.
	if v.kind == KindRef {
		return v.Deref().Copy()
	}
	if v.kind == KindArray && v.a() != nil {
		return Arr(v.a().Clone())
	}
	// Objects are deliberately not copied: they are handles, and PHP assignment
	// shares the instance.
	return v
}

// Share is the copy an assignment makes in compiled code: the same value, with
// an array given a header of its own over the same elements, to be separated
// by whichever holder writes first (Array.Share). It is Copy with the copy
// deferred -- and, unlike Copy, it decides which references survive when the
// separation happens, which is when PHP decides.
//
// Only the compiled path uses it. The interpreter's assignments still Copy, so
// it never meets a shared body.
func (v Value) Share() Value {
	// Small enough to inline: most values are neither an array nor a box.
	if v.kind != KindArray && v.kind != KindRef {
		return v
	}
	return v.shareSlow()
}

func (v Value) shareSlow() Value {
	if v.kind == KindRef {
		return v.Deref().Share()
	}
	if a := v.a(); a != nil {
		return Arr(a.Share())
	}
	return v
}

// ReleaseShared hands back the share a dead holder had on an array, so the
// holder that remains can write in place again (OWNERSHIP-PERF.md P3b).
//
// Only for a value the compiler proved never leaves its frame as itself: a
// by-value parameter that was only read, at the callee's exit. Without it the
// caller's next write copied the whole array, and a loop that appends and then
// passes the array to a function that only reads it was quadratic.
func ReleaseShared(v Value) {
	if v.kind == KindArray {
		v.a().release()
	}
}

// CopyElement is what an array being rebuilt from another takes for one
// element. It is the same rule a whole-array copy follows: a reference two or
// more names hold survives as that reference -- so array_values() over an array
// a foreach left a reference in still has it -- and anything else is copied.
func CopyElement(raw Value) Value {
	if SharedRef(raw) {
		shareCopy(raw)
		return raw
	}
	return raw.Deref().Copy()
}

// Bool applies PHP's truthiness rules, which are not Go's: "0" and "" are
// false, but "0.0" and "false" are true, and an empty array is false.
func (v Value) Bool() bool {
	// The two a comparison yields, ahead of the switch so this inlines:
	// every compiled branch asks it, and a call per loop test was a tenth
	// of a counting loop's time.
	if v.kind == KindTrue {
		return true
	}
	if v.kind == KindFalse {
		return false
	}
	return v.boolSlow()
}

func (v Value) boolSlow() bool {
	switch v.kind {
	case KindUndef, KindNull, KindFalse:
		return false
	case KindTrue:
		return true
	case KindInt:
		return v.num != 0
	case KindFloat:
		return v.f() != 0
	case KindString:
		return v.s() != "" && v.s() != "0"
	case KindArray:
		return v.a() != nil && v.a().Len() > 0
	case KindObject:
		// Every object is truthy; PHP dropped the empty-object exception long
		// ago.
		return true
	case KindResource:
		// So is every resource, open or closed: what is truthy is the handle.
		return true
	}
	return true
}

// Int converts using PHP's rules. A leading numeric prefix is taken and the
// rest ignored, so "12abc" is 12 and "abc" is 0.
func (v Value) Int() int64 {
	if v.kind == KindInt {
		return v.num
	}
	return v.intSlow()
}

func (v Value) intSlow() int64 {
	switch v.kind {
	case KindUndef, KindNull, KindFalse:
		return 0
	case KindTrue:
		return 1
	case KindInt:
		return v.num
	case KindFloat:
		return floatToInt(v.f())
	case KindString:
		i, f, kind := parseNumericPrefix(v.s())
		switch kind {
		case KindInt:
			return i
		case KindFloat:
			// A float that came from a *string* saturates rather than
			// collapsing to zero: (int)"1.0E+121" is PHP_INT_MAX, while
			// (int)1.0E+121 is 0. The engine has two conversions for this.
			return floatToIntCapped(f)
		}
		return 0
	case KindArray:
		if v.a() != nil && v.a().Len() > 0 {
			return 1
		}
		return 0
	case KindResource:
		// A resource casts to its id, which is what makes it usable as an
		// array key and comparable to an integer.
		if v.rs() != nil {
			return int64(v.rs().ID)
		}
		return 0
	}
	return 0
}

// Float converts using PHP's rules.
func (v Value) Float() float64 {
	// Only the float case inline: an int test as well is over the inlining
	// budget, and a boxed operand of float arithmetic is usually a float.
	if v.kind == KindFloat {
		return math.Float64frombits(uint64(v.num))
	}
	return v.floatSlow()
}

func (v Value) floatSlow() float64 {
	switch v.kind {
	case KindInt:
		return float64(v.num)
	case KindFloat:
		return v.f()
	case KindString:
		i, f, kind := parseNumericPrefix(v.s())
		switch kind {
		case KindInt:
			if i == 0 && negativeNumeric(v.s()) {
				// "-0" reads as an integer, but converting a *string* to float
				// goes through the C library, which keeps the sign: the engine
				// prints float(-0) for (float)"-0" and float(0) for (float)0.
				return math.Copysign(0, -1)
			}
			return float64(i)
		case KindFloat:
			return f
		}
		return 0
	}
	return float64(v.Int())
}

// negativeNumeric reports whether a numeric string is written with a leading
// minus, which is the only thing that tells -0 from 0 once it has been read as
// an integer.
func negativeNumeric(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			continue
		case '-':
			return true
		}
		return false
	}
	return false
}

// String renders a value the way PHP does when it needs a string.
func (v Value) String() string {
	switch v.kind {
	case KindUndef, KindNull:
		return ""
	case KindFalse:
		return ""
	case KindTrue:
		return "1"
	case KindInt:
		return strconv.FormatInt(v.num, 10)
	case KindFloat:
		return formatFloat(v.f())
	case KindString:
		return v.s()
	case KindArray:
		// PHP emits "Array" here and raises a notice; the notice belongs to the
		// caller, which has the Ctx.
		return "Array"
	case KindObject:
		// Without __toString this is a fatal error in PHP, which only the
		// caller can raise.
		return ""
	case KindResource:
		return v.rs().String()
	}
	return ""
}

// IsNumericString reports whether a string is entirely numeric, which is
// stricter than what Int and Float accept. PHP uses this distinction to decide
// whether a string compares as a number.
func (v Value) IsNumericString() (Value, bool) {
	if v.kind != KindString {
		return Value{}, false
	}
	trimmed := strings.TrimLeft(v.s(), " \t\n\r\v\f")
	i, f, kind := parseNumericPrefix(trimmed)
	if kind == KindUndef {
		return Value{}, false
	}
	// The prefix has to be the whole string, trailing whitespace aside.
	consumed := numericPrefixLen(trimmed)
	if strings.TrimRight(trimmed[consumed:], " \t\n\r\v\f") != "" {
		return Value{}, false
	}
	if kind == KindInt {
		return Int(i), true
	}
	return Float(f), true
}

// isPHPSpace matches the whitespace the engine skips when reading a number.
func isPHPSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// floatToInt mirrors the engine: out-of-range and non-finite values become 0
// rather than saturating, which is what Go's conversion would do.
func floatToInt(f float64) int64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	if f >= math.MinInt64 && f < twoPow63 {
		return int64(f)
	}
	// Out of range does not collapse to zero and does not saturate: the engine
	// reduces the value modulo 2^64 and reads the result as a signed integer,
	// which is what a C cast would have done where the cast is defined.
	// `(int)1.0E+30` is 5076964154930102272, not 0 and not PHP_INT_MAX.
	dmod := math.Mod(f, twoPow64)
	if dmod < 0 {
		dmod += twoPow64
	}
	if dmod >= twoPow63 {
		dmod -= twoPow64
	}
	return int64(dmod)
}

const (
	twoPow63 = 9223372036854775808.0  // 2^63, one past PHP_INT_MAX
	twoPow64 = 18446744073709551616.0 // 2^64
)

// floatToIntCapped is the engine's zend_dval_to_lval_cap: out of range
// saturates to the nearest bound instead of yielding zero.
func floatToIntCapped(f float64) int64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	if f >= math.MaxInt64 {
		return math.MaxInt64
	}
	if f <= math.MinInt64 {
		return math.MinInt64
	}
	return int64(f)
}

// formatFloat matches PHP's default precision of 14 significant digits, with
// the special spellings PHP uses for non-finite values.
func formatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NAN"
	case math.IsInf(f, 1):
		return "INF"
	case math.IsInf(f, -1):
		return "-INF"
	}
	s := strconv.FormatFloat(f, 'G', 14, 64)
	// Go writes exponents as 1E+15 and 1E-05; PHP writes 1.0E+15 and 1.0E-5
	// -- a lone digit gets `.0`, and the exponent is not padded.
	if i := strings.IndexByte(s, 'E'); i >= 0 {
		mantissa, exp := s[:i], s[i+1:]
		if !strings.Contains(mantissa, ".") {
			mantissa += ".0"
		}
		sign := exp[:1]
		exp = strings.TrimLeft(exp[1:], "0")
		if exp == "" {
			exp = "0"
		}
		s = mantissa + "E" + sign + exp
	}
	return s
}

// numericPrefixLen returns how many bytes of s form a leading number,
// including any whitespace skipped before it. PHP skips leading whitespace, so
// (int)"  8" is 8 rather than 0 — verified against the engine.
func numericPrefixLen(s string) int {
	i := 0
	for i < len(s) && isPHPSpace(s[i]) {
		i++
	}
	if i == len(s) {
		return 0
	}
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	seenDigits := i > digits
	if i < len(s) && s[i] == '.' {
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > i+1 || seenDigits {
			seenDigits = true
			i = j
		}
	}
	if !seenDigits {
		return 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	return i
}

// parseNumericPrefix reads a leading number, reporting whether it is integral
// or floating. kind is KindUndef when there is no number at all.
func parseNumericPrefix(s string) (int64, float64, Kind) {
	n := numericPrefixLen(s)
	if n == 0 {
		return 0, 0, KindUndef
	}
	prefix := strings.TrimLeft(s[:n], " \t\n\r\v\f")
	if !strings.ContainsAny(prefix, ".eE") {
		if i, err := strconv.ParseInt(prefix, 10, 64); err == nil {
			return i, float64(i), KindInt
		}
		// Integer literals that do not fit become floats, as in the engine.
		f, _ := strconv.ParseFloat(prefix, 64)
		return 0, f, KindFloat
	}
	f, err := strconv.ParseFloat(prefix, 64)
	if err != nil {
		// **Out of range is still a number**: "1e1000" is numeric and reads
		// as INF (so `INF == "1e1000"`), "1e-1000" as 0 -- ParseFloat hands
		// back exactly those with a range error.
		if ne, isNum := err.(*strconv.NumError); isNum && ne.Err == strconv.ErrRange {
			return 0, f, KindFloat
		}
		return 0, 0, KindUndef
	}
	return 0, f, KindFloat
}

// KindName is the type name a diagnostic uses: an undefined value is reported
// as null, because that is what PHP calls the absence of a value.
func KindName(v Value) string {
	if v.Kind() == KindUndef {
		return "null"
	}
	return v.Kind().String()
}
