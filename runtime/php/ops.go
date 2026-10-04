package php

import (
	"math"
	"strings"
)

// Arithmetic and comparison follow PHP's rules, which differ from Go's in ways
// that matter: integer overflow promotes to float rather than wrapping, and
// comparison between a number and a numeric string compares numerically.

// Add implements +. Arrays union rather than sum.
// Add is `+`. Two ints that do not overflow take the inlined path; the sum
// grows exactly when the addend is positive, which is the overflow test.
func Add(a, b Value) Value {
	if a.kind == KindInt && b.kind == KindInt {
		if s := a.num + b.num; (s > a.num) == (b.num > 0) {
			return Value{kind: KindInt, num: s}
		}
	}
	return addSlow(a, b)
}

func addSlow(a, b Value) Value {
	if x, y, ok := plainFloats(a, b); ok {
		return Float(x + y)
	}
	if a.Kind() == KindArray || b.Kind() == KindArray {
		if a.Kind() == KindArray && b.Kind() == KindArray {
			return Arr(unionArrays(a.Array(), b.Array()))
		}
		// Mismatched operand types are a TypeError in PHP 8; the caller has the
		// Ctx needed to raise it, so the value result is null.
		return Null()
	}
	if bothInt(a, b) {
		x, y := a.Int(), b.Int()
		sum := x + y
		// Overflow promotes to float instead of wrapping.
		if (x > 0 && y > 0 && sum < 0) || (x < 0 && y < 0 && sum >= 0) {
			return Float(float64(x) + float64(y))
		}
		return Int(sum)
	}
	return Float(a.Float() + b.Float())
}

// Sub is `-`, with Add's inlined path.
func Sub(a, b Value) Value {
	if a.kind == KindInt && b.kind == KindInt {
		if d := a.num - b.num; (d < a.num) == (b.num > 0) {
			return Value{kind: KindInt, num: d}
		}
	}
	return subSlow(a, b)
}

func subSlow(a, b Value) Value {
	if x, y, ok := plainFloats(a, b); ok {
		return Float(x - y)
	}
	if bothInt(a, b) {
		x, y := a.Int(), b.Int()
		diff := x - y
		if (x >= 0 && y < 0 && diff < 0) || (x < 0 && y > 0 && diff >= 0) {
			return Float(float64(x) - float64(y))
		}
		return Int(diff)
	}
	return Float(a.Float() - b.Float())
}

func Mul(a, b Value) Value {
	if x, y, ok := plainFloats(a, b); ok {
		return Float(x * y)
	}
	if bothInt(a, b) {
		x, y := a.Int(), b.Int()
		if x != 0 {
			product := x * y
			// Detect overflow by dividing back out.
			if product/x != y {
				return Float(float64(x) * float64(y))
			}
			return Int(product)
		}
		return Int(0)
	}
	return Float(a.Float() * b.Float())
}

// Div returns the quotient, and false when the divisor is zero — a
// DivisionByZeroError in PHP 8, which only the caller can raise.
func Div(a, b Value) (Value, bool) {
	if x, y, ok := plainFloats(a, b); ok {
		if y == 0 {
			return Null(), false
		}
		return Float(x / y), true
	}
	if bothInt(a, b) {
		x, y := a.Int(), b.Int()
		if y == 0 {
			return Null(), false
		}
		// Exact division stays integral; anything else becomes a float.
		if x%y == 0 && !(x == math.MinInt64 && y == -1) {
			return Int(x / y), true
		}
		return Float(float64(x) / float64(y)), true
	}
	divisor := b.Float()
	if divisor == 0 {
		return Null(), false
	}
	return Float(a.Float() / divisor), true
}

// Concat implements the . operator.
func Concat(a, b Value) Value {
	return String(a.String() + b.String())
}

// plainFloats is the operands of float arithmetic in the commonest case: both
// already numbers and at least one a float, so there is no string to parse
// and no integer result to keep. It answers false for two ints, whose
// arithmetic stays integral.
func plainFloats(a, b Value) (float64, float64, bool) {
	// The float's bits are read straight from num: each kind is tested once
	// here, where going through Value.f tested it again per operand.
	if a.kind == KindFloat {
		x := math.Float64frombits(uint64(a.num))
		switch b.kind {
		case KindFloat:
			return x, math.Float64frombits(uint64(b.num)), true
		case KindInt:
			return x, float64(b.num), true
		}
		return 0, 0, false
	}
	if a.kind == KindInt && b.kind == KindFloat {
		return float64(a.num), math.Float64frombits(uint64(b.num)), true
	}
	return 0, 0, false
}

func bothInt(a, b Value) bool {
	if a.kind == KindInt && b.kind == KindInt {
		return true
	}
	return numericKind(a) == KindInt && numericKind(b) == KindInt
}

// numericKind reports the type an operand contributes to arithmetic. Numeric
// strings participate as numbers.
func numericKind(v Value) Kind {
	switch v.Kind() {
	case KindUndef, KindNull, KindFalse, KindTrue, KindInt:
		return KindInt
	case KindFloat:
		return KindFloat
	case KindString:
		if n, ok := v.IsNumericString(); ok {
			return n.Kind()
		}
		// Non-numeric strings are a TypeError in PHP 8, but treating them as
		// integral keeps the arithmetic total; the caller raises.
		return KindInt
	}
	return KindInt
}

func unionArrays(a, b *Array) *Array {
	result := a.Clone()
	if result == nil {
		result = NewArray()
	}
	// The left operand wins on conflict: + keeps existing keys.
	b.Each(func(k Key, v Value) bool {
		if _, exists := result.Get(k); !exists {
			result.Set(k, v)
		}
		return true
	})
	return result
}

// LooseEqual implements ==. Since PHP 8 a number compared with a non-numeric
// string compares as strings, rather than coercing the string to 0.
func LooseEqual(a, b Value) bool {
	if a.kind == KindInt && b.kind == KindInt {
		return a.num == b.num
	}
	equal, _ := LooseEqualChecked(a, b)
	return equal
}

// LooseEqualChecked is == with the answer to "was this a recursive structure?"
// alongside. The engine raises `Nesting level too deep - recursive dependency?`
// in that case rather than answering, which only a caller with somewhere to
// throw can do.
func LooseEqualChecked(a, b Value) (equal, recursed bool) {
	guard := &recursionGuard{}
	equal = looseEqual(a, b, guard)
	return equal, guard.recursed
}

// recursionGuard records the containers a comparison is already inside.
//
// The engine protects only the *left* container (`zend_hash_compare`,
// `zend_std_compare_objects`): the right one may be reachable from it, and
// protecting both would report a cycle where there is none. Entering a
// protected container again is the cycle, and it is reported rather than
// answered.
type recursionGuard struct {
	inside   map[*arrayBody]bool
	recursed bool
	// objects are the left-hand objects whose properties are being
	// compared. A property view is built fresh for each comparison, so the
	// array guard never saw an object twice and two objects holding
	// themselves compared forever (bug63882).
	objects map[*Object]bool
	// cmp is where a nested element converts through, for `==` asked with a
	// Ctx (comparer); nil for the pure comparison.
	cmp *comparer
}

// enter reports whether the comparison may descend into this pair. It answers
// false once, for the container that closes the cycle.
//
// Both sides are recorded, where the engine records only the left one. It can
// afford to: its VM specialisation decides which operand is which, so the
// container that closes a cycle ends up on the protected side either way.
// Recording both reaches the same answer without that -- the two containers
// being the *same* one is settled by the identity check before this runs, so
// there is no cycle to invent.
func (g *recursionGuard) enter(a, b *Array) bool {
	if g == nil {
		return true
	}
	// Keyed by the elements, not the holder: two headers over one body are
	// one array for the purpose of finding a cycle.
	if g.inside[a.arrayBody] || g.inside[b.arrayBody] {
		g.recursed = true
		return false
	}
	if g.inside == nil {
		g.inside = map[*arrayBody]bool{}
	}
	g.inside[a.arrayBody], g.inside[b.arrayBody] = true, true
	return true
}

func (g *recursionGuard) leave(a, b *Array) {
	if g != nil {
		delete(g.inside, a.arrayBody)
		delete(g.inside, b.arrayBody)
	}
}

// looseEqual carries the guard, so that containers holding one another are
// detected without a package-level counter -- this package keeps no mutable
// state at package scope, because a long-lived process serves many requests.
func looseEqual(a, b Value, guard *recursionGuard) bool {
	// Inside a container, an object beside a string or a number converts as
	// it does at the top level (comparer.convertNested): `['x'] == [new S]`
	// runs `__toString`.
	if guard != nil && guard.cmp != nil && len(guard.inside) > 0 {
		if x, y, ok := guard.cmp.convertNested(a.Deref(), b.Deref()); ok {
			a, b = x, y
		}
		if guard.cmp.err != nil {
			return false
		}
	}
	ka, kb := a.Kind(), b.Kind()

	// null beside a string compares as the empty string rather than through
	// truthiness: `"0" == null` is false, because "0" is not "".
	if isNullish(ka) && kb == KindString {
		return b.RawString() == ""
	}
	if ka == KindString && isNullish(kb) {
		return a.RawString() == ""
	}
	if isBoolish(ka) || isBoolish(kb) {
		return a.Bool() == b.Bool()
	}
	if ka == KindString && kb == KindString {
		na, aNum := a.IsNumericString()
		nb, bNum := b.IsNumericString()
		if aNum && bNum {
			return numericEqual(na, nb)
		}
		return a.RawString() == b.RawString()
	}
	if isNumeric(ka) && kb == KindString {
		if n, ok := b.IsNumericString(); ok {
			return numericEqual(a, n)
		}
		// NAN is uncomparable to a non-numeric string: `NAN == "NAN"` is
		// false, though the float's spelling matches (probed).
		if ka == KindFloat && math.IsNaN(a.Float()) {
			return false
		}
		return a.String() == b.RawString()
	}
	if ka == KindString && isNumeric(kb) {
		return looseEqual(b, a, guard)
	}
	if ka == KindArray && kb == KindArray {
		return arraysEqual(a.Array(), b.Array(), guard)
	}
	if isNumeric(ka) && isNumeric(kb) {
		return numericEqual(a, b)
	}
	if ka == KindResource || kb == KindResource {
		// A resource is its id for the purpose of comparison. Two resources are
		// equal when they are the same one -- `fopen()` twice gives two that
		// are not -- and beside a number, or a numeric string, the resource is
		// that id. Probed: `$r == (int)$r` and `$r == (string)(int)$r` are both
		// true, `$r == "abc"` is false, and so is `$r == []`.
		if ka == kb {
			return a.Resource() == b.Resource()
		}
		if ka == KindArray || kb == KindArray || ka == KindObject || kb == KindObject {
			return false
		}
		return compareAt(a, b, 0) == 0
	}
	// **An object == a number stands in as 1**, the same rule the relational
	// operators use: `1 == $o` is true and `2 == $o` is false. `==` has its own
	// walk rather than going through `compare`, so it needs saying twice.
	if n, ok := objectComparedAsOne(a.Deref(), b.Deref()); ok {
		return n == 0
	}
	if ka == KindObject && kb == KindObject {
		// Two date objects are equal by their time, which is not held.
		refuseDatePair(a.Object(), b.Object())
		// == on objects is same class with equal properties, and the same
		// instance is trivially equal to itself.
		if a.Object() == b.Object() {
			return true
		}
		if a.Object().ClassName != b.Object().ClassName {
			return false
		}
		if a.Object().ClassName == "Closure" {
			// A Closure compares only by identity: two distinct ones are never
			// equal, however alike, and a clone is not equal to what it was
			// cloned from. Probed -- Generator, ArrayObject and WeakMap all
			// compare by properties as any other object does.
			//
			// **Except two made from a callable**: `Closure::fromCallable
			// ("foo")` twice, or `$o->m(...)` twice on one `$o`, are equal --
			// same function, same `$this`, same scope.
			return fakeClosuresEqual(a.Object(), b.Object())
		}
		if eq, handled := containerEqual(a.Object(), b.Object(), guard); handled {
			return eq
		}
		// Two objects that hold each other would compare forever. As
		// zend_std_compare_objects does, the **left** object is protected
		// while its properties are compared, and meeting it again on the
		// left is the cycle (probed: `$a->x = $a; $b->x = $b; $a == $b`,
		// and two objects holding each other, both raise).
		if guard != nil {
			left := a.Object()
			if guard.objects[left] {
				guard.recursed = true
				return false
			}
			if guard.objects == nil {
				guard.objects = map[*Object]bool{}
			}
			guard.objects[left] = true
			defer delete(guard.objects, left)
		}
		return arraysEqual(a.Object().View(), b.Object().View(), guard)
	}
	return ka == kb
}

// FakeClosure is a Closure made from something already callable -- the
// engine's ZEND_ACC_FAKE_CLOSURE -- reporting what identifies it: the
// function (with its class and the called scope folded into key) and the
// bound `$this`. ok is false for a closure written as a literal.
type FakeClosure interface {
	FakeClosureIdentity() (key string, this *Object, ok bool)
}

func fakeClosuresEqual(x, y *Object) bool {
	fx, okx := x.Internal.(FakeClosure)
	fy, oky := y.Internal.(FakeClosure)
	if !okx || !oky {
		return false
	}
	kx, tx, fakeX := fx.FakeClosureIdentity()
	ky, ty, fakeY := fy.FakeClosureIdentity()
	return fakeX && fakeY && kx == ky && tx == ty
}

// maxCompareDepth bounds how far a comparison descends -- through objects that
// contain one another, or a recursive array such as `$a = [&$a]`. PHP raises
// "Nesting level too deep - recursive dependency?" at its own limit; stopping
// here is a gap in the diagnostic but not a hang.
const maxCompareDepth = 64

// StrictEqual implements ===: same type and same value, no coercion.
func StrictEqual(a, b Value) bool {
	// Scalars of one kind need no recursion guard (a heap allocation).
	if a.kind == b.kind {
		switch a.kind {
		case KindInt:
			return a.num == b.num
		case KindString:
			return a.s() == b.s()
		case KindNull, KindTrue, KindFalse:
			return true
		}
	}
	equal, _ := StrictEqualChecked(a, b)
	return equal
}

// StrictEqualValues is `===` as the program writes it: a comparison that
// finds a cycle -- `$a = [&$a]; $a === [[]]` -- is `Nesting level too deep
// - recursive dependency?`, an Error, rather than an answer
// (recursive_array_comparison). Scalars of one kind take the inline path.
func StrictEqualValues(ctx *Ctx, a, b Value) (bool, error) {
	if a.kind == b.kind {
		switch a.kind {
		case KindInt:
			return a.num == b.num, nil
		case KindString:
			return a.s() == b.s(), nil
		case KindNull, KindTrue, KindFalse:
			return true, nil
		}
	}
	equal, recursed := StrictEqualChecked(a, b)
	if recursed {
		return false, RaiseAt(ctx, "Error", "%s", "Nesting level too deep - recursive dependency?")
	}
	return equal, nil
}

// NotStrictEqualValues is `!==`, refusing a cycle as `===` does.
func NotStrictEqualValues(ctx *Ctx, a, b Value) (bool, error) {
	equal, err := StrictEqualValues(ctx, a, b)
	return !equal, err
}

// StrictEqualChecked is === with the recursion answer alongside, as
// LooseEqualChecked is for ==.
func StrictEqualChecked(a, b Value) (equal, recursed bool) {
	guard := &recursionGuard{}
	equal = strictEqual(a, b, guard)
	return equal, guard.recursed
}

func strictEqual(a, b Value, guard *recursionGuard) bool {
	ka, kb := a.Kind(), b.Kind()
	if ka != kb {
		// false and true are distinct kinds but the same PHP *type*; null is
		// its own type, so `null === false` is false however alike they look.
		// undef is the reading of an unset variable, which is null.
		if isBool(ka) && isBool(kb) {
			return a.Bool() == b.Bool()
		}
		if isNullish(ka) && isNullish(kb) {
			return true
		}
		return false
	}
	switch ka {
	case KindUndef, KindNull, KindFalse, KindTrue:
		return true
	case KindInt:
		return a.RawInt() == b.RawInt()
	case KindFloat:
		return a.RawFloat() == b.RawFloat()
	case KindString:
		return a.RawString() == b.RawString()
	case KindArray:
		return arraysIdentical(a.Array(), b.Array(), guard)
	case KindObject:
		// === on objects is identity, not equality: two instances with the same
		// properties are not identical. That is also what makes enum cases,
		// which are singletons, compare correctly.
		return a.Object() == b.Object()
	case KindResource:
		// A resource is a handle, so === is identity here too: `$b = $a` shares
		// the handle and compares identical, while two `fopen()` calls do not.
		return a.Resource() == b.Resource()
	}
	return false
}

// Compare returns -1, 0 or 1 following PHP's ordering.
// Uncomparable reports a pair the engine refuses to order at all. An enum's
// compare handler is zend_objects_not_comparable, and two objects of different
// classes have no ordering either; every relational operator on such a pair is
// false, which is not the same as either side being greater.
func Uncomparable(a, b Value) bool {
	ao, bo := a.Object(), b.Object()
	if ao != nil && bo != nil {
		if ao == bo {
			return false
		}
		if ao.EnumCase != "" || bo.EnumCase != "" {
			return true
		}
		return !strings.EqualFold(ao.ClassName, bo.ClassName)
	}
	// An enum case has no cast to anything, so comparing it with a non-object
	// is refused rather than resolved through a conversion.
	return (ao != nil && ao.EnumCase != "") || (bo != nil && bo.EnumCase != "")
}

func Compare(a, b Value) int { return compareAt(a, b, 0) }

// comparer is what a comparison has besides its two values: the Ctx a nested
// element or property needs to convert, and the first error that conversion
// raised. The nil comparer is the pure comparison.
//
// **Nested values convert as the top level does**, because the engine's
// zend_compare is one function all the way down: `$a > $b` over two objects
// compares `'test'` against a property holding an object with `__toString`,
// and runs it (gh19305); `[5] < [new T]` notices the object's conversion to
// int. The top level converts before it gets here (realizeLazyPair,
// noticeObjectComparison), so this acts below it only.
type comparer struct {
	ctx *Ctx
	err error
	// leftArrays and leftObjects are the left-hand containers being walked:
	// re-entering one on the left is the engine's "Nesting level too deep"
	// (zend_hash_compare and zend_std_compare_objects protect op1).
	leftArrays  map[*arrayBody]bool
	leftObjects map[*Object]bool
}

// nested raises the cycle Error once and reports that the walk must stop.
func (c *comparer) nested() {
	if c.err == nil {
		c.err = RaiseAt(c.ctx, "Error", "%s", "Nesting level too deep - recursive dependency?")
	}
}

// compareAt carries the nesting depth so that containers holding one another
// are bounded.
func compareAt(a, b Value, depth int) int { return (*comparer)(nil).at(a, b, depth) }

// convertNested is the object-against-scalar conversion a nested comparison
// performs, answering the value the object compares as. ok is false where
// there is nothing to convert.
func (c *comparer) convertNested(a, b Value) (Value, Value, bool) {
	if c == nil || c.ctx == nil {
		return a, b, false
	}
	object, other, left := a, b, true
	if b.Kind() == KindObject && a.Kind() != KindObject {
		object, other, left = b, a, false
	} else if a.Kind() != KindObject || b.Kind() == KindObject {
		return a, b, false
	}
	o := object.Object()
	if o == nil || o.EnumCase != "" {
		return a, b, false
	}
	switch other.Kind() {
	case KindString:
		s, has, err := c.ctx.ObjectToString(o)
		if err != nil {
			c.err = err
			return a, b, false
		}
		if !has {
			return a, b, false
		}
		if left {
			return s, b, true
		}
		return a, s, true
	case KindInt, KindFloat:
		kind := "int"
		if other.Kind() == KindFloat {
			kind = "float"
		}
		c.ctx.Notice("Object of class %s could not be converted to %s", o.ClassName, kind)
	}
	return a, b, false
}

func (c *comparer) at(a, b Value, depth int) int {
	if c != nil && c.err != nil {
		return 0
	}
	if depth > 0 {
		if x, y, ok := c.convertNested(a, b); ok {
			return c.at(x, y, depth)
		} else if c != nil && c.err != nil {
			return 0
		}
	}
	if a.Kind() == KindString && b.Kind() == KindString {
		na, aNum := a.IsNumericString()
		nb, bNum := b.IsNumericString()
		if aNum && bNum {
			return compareNumeric(na, nb)
		}
		return compareStrings(a.RawString(), b.RawString())
	}
	// null beside a *string* compares as the empty string, not as a boolean:
	// `"0" == null` is false, because "0" is not "". Every other pairing with
	// null or a bool goes through truthiness.
	if isNullish(a.Kind()) && b.Kind() == KindString {
		return compareStrings("", b.RawString())
	}
	if a.Kind() == KindString && isNullish(b.Kind()) {
		return compareStrings(a.RawString(), "")
	}
	// **An enum case is uncomparable to everything but itself**, and this has
	// to be asked before the boolish rule below: `$c >= true` on a plain object
	// is `true == true` and so equal, while `Foo::Bar >= true` is false. The
	// boolish rule fired first and made the two agree.
	if isEnumCase(a) || isEnumCase(b) {
		if a.Deref().Kind() == KindObject && b.Deref().Kind() == KindObject &&
			a.Deref().Object() == b.Deref().Object() {
			return 0
		}
		return 1
	}
	if isBoolish(a.Kind()) || isBoolish(b.Kind()) {
		return compareBool(a.Bool(), b.Bool())
	}
	if a.Kind() == KindArray && b.Kind() == KindArray {
		return c.arrays(a.Array(), b.Array(), depth)
	}
	// An object outranks everything that is not an object, an array included.
	// This is checked before the array rule below, which would otherwise put
	// the array first.
	if a.Kind() == KindObject || b.Kind() == KindObject {
		// **Against a number, an object *is* 1.** Measured for every relation
		// in both directions: `1 == $o` is true, `2 > $o` is true, `0 < $o` is
		// true, and `$o > 1` is false. The "object outranks everything" rule
		// below is right for a string, a bool, null, an array or another
		// object, and wrong for an int or a float -- which is where the
		// `Notice: ... could not be converted to int` comes from: the engine
		// is announcing the conversion this comparison performs.
		//
		// An enum case keeps its own rule (uncomparable against everything,
		// answering 1 both ways), so it is not funnelled through here.
		if n, ok := objectComparedAsOne(a, b); ok {
			return n
		}
		return c.objects(a, b, depth)
	}
	// An array is greater than anything that is not one. PHP has no ordering
	// between the two kinds, so it simply puts arrays last.
	if a.Kind() == KindArray {
		return 1
	}
	if b.Kind() == KindArray {
		return -1
	}
	// PHP 8's "saner string to number comparisons": a number beside a string
	// that is *not* numeric is compared as a string, not by casting the string
	// to a number. `0 < "abc"` is true because "0" sorts before "abc"; in PHP 7
	// it was false, because "abc" became 0.
	if isNumeric(a.Kind()) && b.Kind() == KindString {
		if _, numeric := b.IsNumericString(); !numeric {
			// A NAN beside a non-numeric string is uncomparable, which the
			// engine answers as 1 either way round.
			if a.Kind() == KindFloat && math.IsNaN(a.Float()) {
				return 1
			}
			return compareStrings(a.String(), b.RawString())
		}
	}
	if a.Kind() == KindString && isNumeric(b.Kind()) {
		if _, numeric := a.IsNumericString(); !numeric {
			if b.Kind() == KindFloat && math.IsNaN(b.Float()) {
				return 1
			}
			return compareStrings(a.RawString(), b.String())
		}
	}
	return compareNumeric(a, b)
}

// objects orders an object against anything that is not an array or a
// boolean -- those two were settled above. Two objects of the same class are
// compared property by property, in declaration order, the way two arrays
// would be. Anything else is *uncomparable*, and PHP expresses that by calling
// the object greater whichever side it is on -- so `$o < 1` and `$o > 1` are
// both false and `1 < $o` is true.
// objects orders two objects, or an object against something that is
// neither an array nor a boolean. The depth travels as an argument rather than
// in a package variable, the same way looseEqual carries its own: this package
// serves many requests and keeps no mutable state at package scope.
// objectComparedAsOne compares an object against a number by standing the
// object in as 1, reporting false when that is not the shape.
func objectComparedAsOne(a, b Value) (int, bool) {
	numeric := func(v Value) bool { return v.Kind() == KindInt || v.Kind() == KindFloat }
	plain := func(v Value) bool {
		o := v.Object()
		return v.Kind() == KindObject && o != nil && o.EnumCase == ""
	}
	switch {
	case plain(a) && numeric(b):
		return compareAt(Int(1), b, 0), true
	case numeric(a) && plain(b):
		return compareAt(a, Int(1), 0), true
	}
	return 0, false
}

// arrays is zend_hash_compare unordered, which is what `<`, `<=>` and a sort
// ask of two arrays: the smaller count is smaller; otherwise each element of
// the left, in its order, against the right's element under the same key,
// and a key the right lacks makes the pair uncomparable (1, either way
// round). The order the right keeps is not asked: `['a'=>1,'b'=>2] <=>
// ['b'=>2,'a'=>1]` is 0. Probed, with nesting and against objects.
//
// It compared the counts alone, so `[1, 2] < [1, 3]` was false and sort()
// left a list of lists in whatever order it found it.
func (c *comparer) arrays(x, y *Array, depth int) int {
	if x.Identity() == y.Identity() {
		return 0
	}
	if c != nil {
		if c.leftArrays[x.arrayBody] {
			c.nested()
			return 0
		}
		if c.leftArrays == nil {
			c.leftArrays = map[*arrayBody]bool{}
		}
		c.leftArrays[x.arrayBody] = true
		defer delete(c.leftArrays, x.arrayBody)
	}
	if n := compareInts(int64(x.Len()), int64(y.Len())); n != 0 {
		return n
	}
	if depth >= maxCompareDepth {
		return 0
	}
	result := 0
	x.Each(func(k Key, v Value) bool {
		other, present := y.Get(k)
		if !present {
			result = 1
			return false
		}
		if n := c.at(v, other, depth+1); n != 0 {
			result = n
			return false
		}
		return c == nil || c.err == nil
	})
	return result
}

func (c *comparer) objects(a, b Value, depth int) int {
	x, y := a.Object(), b.Object()
	// **An enum case is compared by identity alone**, and against *anything*:
	// not only another case but a bool, which is where it differs from an
	// ordinary object. `$c > true` on a plain object is `true == true`, so
	// `>=` and `<=` are both true; `Foo::Bar > true` is false in all four
	// directions. Two cases of one enum would otherwise be ordered by their
	// `name` properties -- same class, different strings.
	//
	// 1 in **both** directions is what "uncomparable" is: the relational
	// operators are `is_smaller(b,a)` and `is_smaller_or_equal(b,a)`, so a
	// compare that answers 1 whichever way round it is asked makes all four
	// false. Measured for every operator against a case, a bool and null.
	if (x != nil && x.EnumCase != "") || (y != nil && y.EnumCase != "") {
		if x != nil && x == y {
			return 0
		}
		return 1
	}
	if x == nil || y == nil {
		if x != nil {
			return 1
		}
		return -1
	}
	if x == y {
		return 0
	}
	// Two date objects are ordered by their time, which is not held.
	refuseDatePair(x, y)
	if n, handled := c.storageObjects(x, y, depth); handled {
		return n
	}
	if !strings.EqualFold(x.ClassName, y.ClassName) {
		return 1
	}
	// An ArrayObject or ArrayIterator compares its storage first
	// (spl_array_compare_objects), then its properties.
	if xs, ys := arrayStoreForCompare(x), arrayStoreForCompare(y); xs != nil && ys != nil {
		if n := c.arrays(xs, ys, depth); n != 0 {
			return n
		}
	}
	if c != nil {
		if c.leftObjects[x] {
			c.nested()
			return 0
		}
		if c.leftObjects == nil {
			c.leftObjects = map[*Object]bool{}
		}
		c.leftObjects[x] = true
		defer delete(c.leftObjects, x)
	}
	if c := compareInts(int64(x.PropLen()), int64(y.PropLen())); c != 0 {
		return c
	}
	if depth >= maxCompareDepth {
		return 0
	}
	result := 0
	x.PropEach(func(k Key, v Value) bool {
		other, present := y.PropGet(k)
		if !present {
			result = 1
			return false
		}
		if n := c.at(v.Deref(), other.Deref(), depth+1); n != 0 {
			result = n
			return false
		}
		return c == nil || c.err == nil
	})
	return result
}

// isEnumCase reports whether a value is an enum case, which compares by
// identity and nothing else.
func isEnumCase(v Value) bool {
	d := v.Deref()
	return d.Kind() == KindObject && d.Object() != nil && d.Object().EnumCase != ""
}

func isBoolish(k Kind) bool {
	return k == KindFalse || k == KindTrue || k == KindNull || k == KindUndef
}

// isBool is the two boolean kinds only, which `===` treats as one type.
func isBool(k Kind) bool { return k == KindFalse || k == KindTrue }

// isNullish is null and the undef an unset variable reads as.
func isNullish(k Kind) bool { return k == KindNull || k == KindUndef }

func isNumeric(k Kind) bool { return k == KindInt || k == KindFloat }

func numericEqual(a, b Value) bool {
	if a.Kind() == KindInt && b.Kind() == KindInt {
		return a.Int() == b.Int()
	}
	return a.Float() == b.Float()
}

func compareNumeric(a, b Value) int {
	if a.Kind() == KindInt && b.Kind() == KindInt {
		return compareInts(a.Int(), b.Int())
	}
	x, y := a.Float(), b.Float()
	switch {
	case x < y:
		return -1
	case x == y:
		return 0
	}
	// Greater -- or unordered: the engine's ZEND_THREEWAY_COMPARE answers 1
	// when either side is NAN, so `NAN <= 1`, `NAN <= NAN` and `NAN == NAN`
	// are all false and `NAN <=> x` is 1 from either side. Answering 0 made
	// them true.
	return 1
}

func compareInts(x, y int64) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

func compareStrings(x, y string) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

func compareBool(x, y bool) int {
	switch {
	case x == y:
		return 0
	case !x:
		return -1
	}
	return 1
}

func arraysEqual(a, b *Array, guard *recursionGuard) bool {
	if a == b {
		return true
	}
	// The guard comes before the length check, as the engine's recursion
	// protection comes before `zend_hash_compare_impl`: comparing a recursive
	// array with one of a different size is still a cycle, not a "no".
	if !guard.enter(a, b) {
		return false
	}
	defer guard.leave(a, b)
	if a.Len() != b.Len() {
		return false
	}
	equal := true
	a.Each(func(k Key, v Value) bool {
		other, ok := b.Get(k)
		if !ok || !looseEqual(v, other, guard) {
			equal = false
			return false
		}
		return true
	})
	return equal
}

// arraysIdentical also requires the same order, which == does not.
func arraysIdentical(a, b *Array, guard *recursionGuard) bool {
	// The same array is identical to itself without looking inside, which is
	// what makes `$a === $a` answer even when $a contains itself.
	if a == b {
		return true
	}
	// A recursive array has no bottom: the guard stops at the container that
	// closes the cycle and records that it did. It runs before the length
	// check, as the engine's recursion protection does.
	if !guard.enter(a, b) {
		return false
	}
	defer guard.leave(a, b)
	if a.Len() != b.Len() {
		return false
	}
	bKeys := b.Keys()
	identical := true
	i := 0
	a.Each(func(k Key, v Value) bool {
		if i >= len(bKeys) || bKeys[i] != k {
			identical = false
			return false
		}
		other, _ := b.Get(k)
		if !strictEqual(v, other, guard) {
			identical = false
			return false
		}
		i++
		return true
	})
	return identical
}

// Mod is PHP's %, which is integer-only: both operands are cast to int first,
// so 7.5 % 2 is 1 rather than 1.5. It reports false for a zero divisor, which
// the caller turns into a DivisionByZeroError.
func Mod(a, b Value) (Value, bool) {
	x, y := a.Int(), b.Int()
	if y == 0 {
		return Null(), false
	}
	// Go panics on MinInt64 % -1 where PHP yields 0.
	if y == -1 {
		return Int(0), true
	}
	return Int(x % y), true
}

// Pow is PHP's ** operator. An integer base and a non-negative integer exponent
// stay integral unless the result overflows, in which case PHP switches to
// float rather than wrapping.
func Pow(a, b Value) Value {
	if bothInt(a, b) && b.Int() >= 0 {
		base, exp := a.Int(), b.Int()
		result := int64(1)
		overflow := false
		for i := int64(0); i < exp; i++ {
			next := result * base
			// Detecting overflow by division is exact for integers, unlike
			// comparing against a float approximation.
			if base != 0 && next/base != result {
				overflow = true
				break
			}
			result = next
		}
		if !overflow {
			return Int(result)
		}
	}
	return Float(math.Pow(a.Float(), b.Float()))
}

// BitAnd, BitOr and BitXor operate on integers. PHP also defines them bytewise
// for two strings; that form is not handled here and would need its own path.
// The three binary bitwise operators work on *bytes* when both operands are
// strings, and on integers otherwise. That is not a coercion: "abc" & "def" is
// a three-byte string, and neither operand is read as a number at all.
//
// The lengths do not agree in general, and the operators disagree about what to
// do then. AND and XOR stop at the shorter one, since a missing byte is zero
// and zero wins for both. OR keeps the longer one's tail, since zero loses.

func BitAnd(a, b Value) Value {
	if left, right, ok := bothStrings(a, b); ok {
		return String(byteWise(left, right, false, func(x, y byte) byte { return x & y }))
	}
	return Int(a.Int() & b.Int())
}

func BitOr(a, b Value) Value {
	if left, right, ok := bothStrings(a, b); ok {
		return String(byteWise(left, right, true, func(x, y byte) byte { return x | y }))
	}
	return Int(a.Int() | b.Int())
}

func BitXor(a, b Value) Value {
	if left, right, ok := bothStrings(a, b); ok {
		return String(byteWise(left, right, false, func(x, y byte) byte { return x ^ y }))
	}
	return Int(a.Int() ^ b.Int())
}

// bothStrings reports the two operands as strings when both really are strings.
// A numeric string counts: "12" & "34" is byte work, not arithmetic.
func bothStrings(a, b Value) (string, string, bool) {
	if a.Kind() != KindString || b.Kind() != KindString {
		return "", "", false
	}
	return a.RawString(), b.RawString(), true
}

// byteWise combines two byte strings. keepTail says whether the longer
// operand's remaining bytes survive, which is what separates OR from the other
// two.
func byteWise(a, b string, keepTail bool, combine func(byte, byte) byte) string {
	shortest, longest := a, b
	if len(b) < len(a) {
		shortest, longest = b, a
	}
	out := make([]byte, len(shortest))
	for i := range out {
		out[i] = combine(a[i], b[i])
	}
	if keepTail {
		out = append(out, longest[len(shortest):]...)
	}
	return string(out)
}

// BitNotValue is `~` with somewhere to report the conversion it performs.
//
// `~1.5` converts to int and **deprecates the precision loss**, exactly as the
// binary integral operators do. They get there through `AcceptsArithmetic`,
// which takes two operands and so cannot serve a unary one -- which is why this
// was the only bitwise operator still silent, and why the catalogue entry for
// `BitNot` lacked the `Deprecates` effect its siblings all carry.
func BitNotValue(ctx *Ctx, a Value) (Value, error) {
	// **Only a string and the numbers have a complement.** Anything else is
	// a TypeError naming the value -- `null`, `true`, `array`, the class of an
	// object -- measured for every kind.
	switch a.Deref().Kind() {
	case KindString, KindInt, KindFloat:
	default:
		return Value{}, Raise("TypeError", "Cannot perform bitwise not on %s", ValueName(a.Deref()))
	}
	// **A string is bytewise, whatever it looks like.** `~"1.5"` complements
	// three bytes; it does not convert to a number, so there is no precision to
	// lose and nothing to say. The shared `warnPrecisionLoss` handles a
	// float-shaped string because the *binary* operators do convert one, which
	// makes it exactly the wrong helper to apply here unguarded.
	if a.Deref().Kind() != KindString {
		warnPrecisionLoss(ctx, a.Deref())
	}
	return BitNot(a), nil
}

func BitNot(a Value) Value {
	if a.Kind() == KindString {
		// Like the binary operators, ~ on a string is byte work rather than a
		// conversion: every byte is complemented and the length is unchanged.
		out := []byte(a.RawString())
		for i, c := range out {
			out[i] = ^c
		}
		return String(string(out))
	}
	return Int(^a.Int())
}

// ShiftLeft and ShiftRight report false for a negative shift count, which PHP
// raises as an ArithmeticError. A count of 64 or more yields 0 (or the sign bit
// for a right shift) rather than Go's undefined-for-oversized behaviour.
func ShiftLeft(a, b Value) (Value, bool) {
	count := b.Int()
	if count < 0 {
		return Null(), false
	}
	if count >= 64 {
		return Int(0), true
	}
	return Int(a.Int() << uint(count)), true
}

func ShiftRight(a, b Value) (Value, bool) {
	count := b.Int()
	if count < 0 {
		return Null(), false
	}
	value := a.Int()
	if count >= 64 {
		// An arithmetic shift keeps the sign, so a negative value saturates at
		// -1 rather than 0.
		if value < 0 {
			return Int(-1), true
		}
		return Int(0), true
	}
	return Int(value >> uint(count)), true
}

// Operator symbols, as PHP spells them in "Unsupported operand types" and as
// the arithmetic checks below switch on.
const (
	OpAdd        = "+"
	OpSub        = "-"
	OpMul        = "*"
	OpDiv        = "/"
	OpMod        = "%"
	OpPow        = "**"
	OpBitAnd     = "&"
	OpBitOr      = "|"
	OpBitXor     = "^"
	OpShiftLeft  = "<<"
	OpShiftRight = ">>"
)

// AcceptsArithmetic reports whether two operands may take part in an arithmetic
// or bitwise operation, raising the diagnostics PHP raises on the way.
//
// PHP 8 draws three lines where PHP 7 drew one: a wholly numeric string is
// silently converted, a string that merely *starts* with a number is converted
// with a warning, and anything else -- including "abc", "" and any array or
// object -- is a TypeError. Returning false leaves the caller to throw one,
// since only it can build an exception.
// AcceptsArithmetic's two-int answer, small enough to inline at every
// arithmetic site; everything else asks acceptsArithmeticSlow.
func AcceptsArithmetic(c *Ctx, symbol string, a, b Value) bool {
	if a.kind == KindInt && b.kind == KindInt {
		return true
	}
	return acceptsArithmeticSlow(c, symbol, a, b)
}

func acceptsArithmeticSlow(c *Ctx, symbol string, a, b Value) bool {
	// Two ints convert to nothing and warn about nothing, under any operator;
	// two plain numbers likewise, except that the integer operators report a
	// float losing precision, which the walk below does.
	if a.kind == KindInt && b.kind == KindInt {
		return true
	}
	if (a.kind == KindInt || a.kind == KindFloat) && (b.kind == KindInt || b.kind == KindFloat) {
		switch symbol {
		case OpMod, OpBitAnd, OpBitOr, OpBitXor, OpShiftLeft, OpShiftRight:
		default:
			return true
		}
	}
	// Two arrays add by union, and two strings combine bytewise under the
	// bitwise operators. Neither goes through numeric conversion at all.
	switch symbol {
	case OpAdd:
		if a.Kind() == KindArray && b.Kind() == KindArray {
			return true
		}
	case OpBitAnd, OpBitOr, OpBitXor:
		if a.Kind() == KindString && b.Kind() == KindString {
			return true
		}
	}
	// Conversion is strictly left to right, and each operand's diagnostic comes
	// out as it converts. That ordering is observable twice over: an operand
	// that cannot convert at all stops the walk, so a *later* one never gets to
	// warn -- `"" % "2abc"` is a bare TypeError, with no "non-numeric value"
	// for the "2abc" -- and an earlier one's warning survives the refusal, as
	// `2.5 % ""` shows by reporting the precision loss and then failing.
	integral := false
	switch symbol {
	// The operators that work on integers report a float operand that will not
	// survive the conversion. The others keep the float, so they say nothing.
	case OpMod, OpBitAnd, OpBitOr, OpBitXor, OpShiftLeft, OpShiftRight:
		integral = true
	}
	for _, operand := range [...]Value{a, b} {
		if !acceptsNumeric(c, operand) {
			return false
		}
		if integral {
			warnPrecisionLoss(c, operand)
		}
	}
	return true
}

// acceptsNumeric reports whether one operand can be read as a number, warning
// about a string that only starts with one.
func acceptsNumeric(c *Ctx, v Value) bool {
	switch v.Kind() {
	case KindUndef, KindNull, KindFalse, KindTrue, KindInt, KindFloat:
		return true
	case KindString:
		if _, numeric := v.IsNumericString(); numeric {
			return true
		}
		if numericPrefixLen(v.s()) == 0 {
			return false
		}
		c.Warn("A non-numeric value encountered")
		return true
	}
	return false
}

// warnPrecisionLoss reports a float, or a float-shaped string, that an integer
// operator is about to truncate. PHP words the two cases differently.
// WarnPrecisionLoss reports a float that will not survive conversion to an
// integer, which the integral operators say before doing the work.
func WarnPrecisionLoss(c *Ctx, v Value) { warnPrecisionLoss(c, v) }

func warnPrecisionLoss(c *Ctx, v Value) {
	switch v.Kind() {
	case KindFloat:
		if f := v.Float(); f == math.Trunc(f) && !math.IsInf(f, 0) {
			return
		}
		// Spelled at full precision, as var_dump spells it -- the engine's
		// `%.*H` with -1 -- not echo's 14 digits (isset_str_offset).
		c.Deprecated("Implicit conversion from float %s to int loses precision", DumpFloat(v.Float()))
	case KindString:
		numeric, ok := v.IsNumericString()
		if !ok || numeric.Kind() != KindFloat {
			return
		}
		if f := numeric.Float(); f == math.Trunc(f) && !math.IsInf(f, 0) {
			return
		}
		c.Deprecated("Implicit conversion from float-string \"%s\" to int loses precision", v.s())
	}
}

// TypeName is how PHP names a value's type in an operand error. It differs from
// Kind.String only for an object, which is named by its class.
func TypeName(v Value) string {
	switch v.Kind() {
	case KindUndef:
		return "null"
	case KindObject:
		return v.Object().ClassName
	}
	return v.Kind().String()
}

// Comparing an object with a number is a **Notice**, and only with a number.
//
// Measured against the engine, every combination:
//
//	1 > $o, $o > 1, 1 <= $o, $o == 1   Notice: ... could not be converted to int
//	2.5 > $o, $o > 2.5                 Notice: ... could not be converted to float
//	"s" > $o, true > $o, null > $o,
//	[1] > $o, $o2 > $o                 nothing at all
//
// A `__toString` does not excuse it: `1 > new S` notices exactly as `1 > new T`
// does, because the conversion being attempted is to a number, not a string.
// An explicit `(int)$o` raises a **Warning** rather than a Notice, which is a
// different diagnostic for a different operation and is not this one.
//
// The catalogue has declared `Effects: Warns` on the comparison intrinsics all
// along; the value functions simply had no `Ctx` to say it with, so the
// declaration described behaviour nothing implemented.
func noticeObjectComparison(ctx *Ctx, a, b Value) {
	da, db := a.Deref(), b.Deref()
	object, other := da, db
	if db.Kind() == KindObject {
		if da.Kind() == KindObject {
			return // both objects: no conversion is attempted
		}
		object, other = db, da
	} else if da.Kind() != KindObject {
		return
	}
	kind := ""
	switch other.Kind() {
	case KindInt:
		kind = "int"
	case KindFloat:
		kind = "float"
	default:
		// Everything else -- string, bool, null, array, another object --
		// compares without attempting a numeric conversion, and says nothing.
		return
	}
	o := object.Object()
	if o == nil || o.EnumCase != "" {
		// **An enum case is never converted**, so there is nothing to announce:
		// it is uncomparable against a number rather than standing in as 1, and
		// the engine says nothing at all. Measured: `E::A > 1`, `1 > E::A`,
		// `E::A == 1` and `F::X == 1` are all false and all silent -- including
		// a backed enum whose value *is* 1.
		return
	}
	ctx.Notice("Object of class %s could not be converted to %s", o.ClassName, kind)
}

// CompareValues is `Compare` with somewhere to raise the notice.
func CompareValues(ctx *Ctx, a, b Value) (int, error) {
	a, b, err := realizeLazyPair(ctx, a, b)
	if err != nil {
		return 0, err
	}
	noticeObjectComparison(ctx, a, b)
	return compareIn(ctx, a, b)
}

// compareIn is Compare with a Ctx for what nested values convert through.
func compareIn(ctx *Ctx, a, b Value) (int, error) {
	c := &comparer{ctx: ctx}
	n := c.at(a, b, 0)
	return n, c.err
}

// realizeLazyPair initializes lazy objects about to be compared with another
// object: comparing reads their properties, so a ghost runs its initializer and
// a proxy compares as the instance behind it -- and an initializer's exception
// leaves the comparison. Only object against object; a string comparison goes
// through `__toString` and initializes nothing.
func realizeLazyPair(ctx *Ctx, a, b Value) (Value, Value, error) {
	x, y := a.Deref(), b.Deref()
	// **An object against a string is cast to one first**, which is the
	// engine's compare handler: `$o > ''` runs `__toString` and compares the
	// strings, and a `__toString` that fails leaves the comparison with its
	// own error. An object with no `__toString` keeps the pure rule -- it is
	// greater, whichever side it is on.
	if x.Kind() == KindObject && y.Kind() == KindString && !isEnumCase(x) {
		if s, has, err := ctx.ObjectToString(x.Object()); err != nil {
			return a, b, err
		} else if has {
			return s, b, nil
		}
		return a, b, nil
	}
	if y.Kind() == KindObject && x.Kind() == KindString && !isEnumCase(y) {
		if s, has, err := ctx.ObjectToString(y.Object()); err != nil {
			return a, b, err
		} else if has {
			return a, s, nil
		}
		return a, b, nil
	}
	if x.Kind() != KindObject || y.Kind() != KindObject || x.Object() == y.Object() {
		return a, b, nil
	}
	if x.Object().Lazy == nil && y.Object().Lazy == nil {
		return a, b, nil
	}
	rx, err := ResolveLazy(ctx, x.Object(), "", "")
	if err != nil {
		return a, b, err
	}
	ry, err := ResolveLazy(ctx, y.Object(), "", "")
	if err != nil {
		return a, b, err
	}
	return Obj(rx), Obj(ry), nil
}

// LooseEqualValues is `==` with somewhere to raise the notice.
// LooseEqualValues is `==`; two ints inline.
func LooseEqualValues(ctx *Ctx, a, b Value) (bool, error) {
	if a.kind == KindInt && b.kind == KindInt {
		return a.num == b.num, nil
	}
	return looseEqualValuesSlow(ctx, a, b)
}

func looseEqualValuesSlow(ctx *Ctx, a, b Value) (bool, error) {
	a, b, err := realizeLazyPair(ctx, a, b)
	if err != nil {
		return false, err
	}
	if a.kind == KindInt && b.kind == KindInt {
		return a.num == b.num, nil
	}
	noticeObjectComparison(ctx, a, b)
	return looseEqualIn(ctx, a, b)
}

// looseEqualIn is LooseEqual with a Ctx for what nested values convert
// through.
func looseEqualIn(ctx *Ctx, a, b Value) (bool, error) {
	guard := &recursionGuard{cmp: &comparer{ctx: ctx}}
	equal := looseEqual(a, b, guard)
	if guard.cmp.err != nil {
		return equal, guard.cmp.err
	}
	// A cycle is an Error, not an answer, as it is for === (it answered
	// false: `$p = [&$p]; $p == $q`).
	if guard.recursed {
		return false, RaiseAt(ctx, "Error", "%s", "Nesting level too deep - recursive dependency?")
	}
	return equal, nil
}

// NotLooseEqualValues is `!=`, which converts exactly as `==` does.
// NotLooseEqualValues is `!=`; two ints inline.
func NotLooseEqualValues(ctx *Ctx, a, b Value) (bool, error) {
	if a.kind == KindInt && b.kind == KindInt {
		return a.num != b.num, nil
	}
	return notLooseEqualValuesSlow(ctx, a, b)
}

func notLooseEqualValuesSlow(ctx *Ctx, a, b Value) (bool, error) {
	a, b, err := realizeLazyPair(ctx, a, b)
	if err != nil {
		return false, err
	}
	if a.kind == KindInt && b.kind == KindInt {
		return a.num != b.num, nil
	}
	noticeObjectComparison(ctx, a, b)
	equal, err := looseEqualIn(ctx, a, b)
	return !equal, err
}

// IsSmallerValues is `<`.
// IsSmallerValues is `<`; two ints inline.
func IsSmallerValues(ctx *Ctx, a, b Value) (bool, error) {
	if a.kind == KindInt && b.kind == KindInt {
		return a.num < b.num, nil
	}
	return isSmallerValuesSlow(ctx, a, b)
}

func isSmallerValuesSlow(ctx *Ctx, a, b Value) (bool, error) {
	a, b, err := realizeLazyPair(ctx, a, b)
	if err != nil {
		return false, err
	}
	// Two integers, the loop bound: nothing to convert and nothing to
	// notice, and the general comparison asks a dozen questions first.
	if a.kind == KindInt && b.kind == KindInt {
		return a.num < b.num, nil
	}
	// An int beside a float compares as two doubles, as the engine does.
	if x, y, ok := plainFloats(a, b); ok {
		return x < y, nil
	}
	noticeObjectComparison(ctx, a, b)
	n, err := compareIn(ctx, a, b)
	return n < 0, err
}

// IsSmallerOrEqualValues is `<=`.
// IsSmallerOrEqualValues is `<=`; two ints inline.
func IsSmallerOrEqualValues(ctx *Ctx, a, b Value) (bool, error) {
	if a.kind == KindInt && b.kind == KindInt {
		return a.num <= b.num, nil
	}
	return isSmallerOrEqualValuesSlow(ctx, a, b)
}

func isSmallerOrEqualValuesSlow(ctx *Ctx, a, b Value) (bool, error) {
	a, b, err := realizeLazyPair(ctx, a, b)
	if err != nil {
		return false, err
	}
	if a.kind == KindInt && b.kind == KindInt {
		return a.num <= b.num, nil
	}
	if x, y, ok := plainFloats(a, b); ok {
		return x <= y, nil
	}
	noticeObjectComparison(ctx, a, b)
	n, err := compareIn(ctx, a, b)
	return n <= 0, err
}

// splStorageKind reports an SplObjectStorage or a subclass of it, whose
// compare handler is the container's own.
func splStorageKind(o *Object) bool {
	if _, ok := o.Internal.(*SplStorage); ok {
		return true
	}
	return o.Internal == nil && SplKindOf(o.ClassName) == "SplObjectStorage"
}

// arrayStoreForCompare is the storage an ArrayObject or ArrayIterator
// compares before its properties.
func arrayStoreForCompare(o *Object) *Array {
	if store, ok := AsArrayStore(o); ok && store.items != nil {
		return store.items
	}
	return nil
}

// storageObjects is spl_object_storage_compare_objects: only two objects of
// SplObjectStorage itself -- not a subclass -- are comparable, by their
// storage alone (the objects held and, for each, the info), and nothing else
// of either is looked at. handled is false for any other pair.
func (c *comparer) storageObjects(x, y *Object, depth int) (int, bool) {
	if !splStorageKind(x) && !splStorageKind(y) {
		return 0, false
	}
	if x.ClassName != "SplObjectStorage" || y.ClassName != "SplObjectStorage" {
		return 1, true
	}
	xs, ys := SplStorageOf(x), SplStorageOf(y)
	if n := compareInts(int64(len(xs.entries)), int64(len(ys.entries))); n != 0 {
		return n, true
	}
	for _, e := range xs.entries {
		i := ys.find(e.obj)
		if i < 0 {
			return 1, true
		}
		if n := c.at(e.inf, ys.entries[i].inf, depth+1); n != 0 {
			return n, true
		}
	}
	return 0, true
}

// containerEqual is `==` for the containers whose compare handler is their
// own: storageObjects for SplObjectStorage, and an ArrayObject's or
// ArrayIterator's storage before its properties. handled is false for any
// other pair.
func containerEqual(x, y *Object, guard *recursionGuard) (bool, bool) {
	if splStorageKind(x) || splStorageKind(y) {
		if x.ClassName != "SplObjectStorage" || y.ClassName != "SplObjectStorage" {
			return false, true
		}
		xs, ys := SplStorageOf(x), SplStorageOf(y)
		if len(xs.entries) != len(ys.entries) {
			return false, true
		}
		for _, e := range xs.entries {
			i := ys.find(e.obj)
			if i < 0 || !looseEqual(e.inf, ys.entries[i].inf, guard) {
				return false, true
			}
		}
		return true, true
	}
	if xs, ys := arrayStoreForCompare(x), arrayStoreForCompare(y); xs != nil && ys != nil && !arraysEqual(xs, ys, guard) {
		return false, true
	}
	return false, false
}
