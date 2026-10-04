package php_test

import (
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

func ptr(t php.Type) *php.Type { return &t }

// TestCovarianceOnElements. A container is a subtype of the same container over
// a **wider** element type, which is what lets a variant declared over
// `array<numeric>` apply to an `array<int>`.
//
// Covariance is sound for a *read* and not for a write: appending a string to
// an `array<int>` widens it, which is why `array.append` unions rather than
// checking containment.
func TestCovarianceOnElements(t *testing.T) {
	intArr := php.ArrayOf(nil, ptr(php.Of(php.MaskInt)))
	numArr := php.ArrayOf(nil, ptr(php.Of(php.MaskNumeric)))
	strArr := php.ArrayOf(nil, ptr(php.Of(php.MaskString)))

	if !numArr.Subsumes(intArr) {
		t.Error("array<int> is not accepted as array<numeric>; covariance is the point")
	}
	if intArr.Subsumes(numArr) {
		t.Error("array<numeric> was accepted as array<int>; that is contravariance and it is wrong")
	}
	if numArr.Subsumes(strArr) {
		t.Error("array<string> was accepted as array<numeric>")
	}
	if !intArr.Subsumes(intArr) {
		t.Error("a type does not subsume itself")
	}
}

// TestUnknownElementsAreTheTop is the soundness crux.
//
// An array we have not looked inside may hold anything, so it satisfies a
// constraint on elements only when there is no constraint. Treating unknown as
// *empty* -- the natural reading if you think of nil as a zero value -- would
// let `array<int>` accept an array of strings, and the narrow variant would
// skip a diagnostic PHP emits.
func TestUnknownElementsAreTheTop(t *testing.T) {
	unknown := php.Of(php.MaskArray)
	intArr := php.ArrayOf(nil, ptr(php.Of(php.MaskInt)))

	if intArr.Subsumes(unknown) {
		t.Error("an array of unknown elements was accepted as array<int>")
	}
	if !unknown.Subsumes(intArr) {
		t.Error("array<int> is not an array; an unconstrained container accepts any")
	}
	// And the union of known with unknown is unknown, not the known one.
	if u := php.Union(unknown, intArr); u.Elem != nil {
		t.Errorf("union with an unknown element type kept an element type: %s", u)
	}
}

// TestKeysAreCheckedToo. `array<string, int>` and `array<int, int>` differ, and
// a foreach over one binds a different key type.
func TestKeysAreCheckedToo(t *testing.T) {
	strKeyed := php.ArrayOf(ptr(php.Of(php.MaskString)), ptr(php.Of(php.MaskInt)))
	intKeyed := php.ArrayOf(ptr(php.Of(php.MaskInt)), ptr(php.Of(php.MaskInt)))
	anyKeyed := php.ArrayOf(ptr(php.Of(php.MaskString|php.MaskInt)), ptr(php.Of(php.MaskInt)))

	if strKeyed.Subsumes(intKeyed) {
		t.Error("an int-keyed array was accepted where the key must be a string")
	}
	if !anyKeyed.Subsumes(strKeyed) || !anyKeyed.Subsumes(intKeyed) {
		t.Error("a widened key type did not accept either narrower one")
	}
}

// TestIteratorsCarryWhatTheyYield. The cursor lives in an object, so an
// iterator's mask says object and its parameters say what stepping produces.
func TestIteratorsCarryWhatTheyYield(t *testing.T) {
	it := php.IterOf(ptr(php.Of(php.MaskInt)), ptr(php.Of(php.MaskString)))
	if it.Mask != php.MaskObject {
		t.Errorf("an iterator is %s, want object", it.Mask)
	}
	if !php.Of(php.MaskObject).Subsumes(it) {
		t.Error("an iterator is not an object")
	}
	wider := php.IterOf(ptr(php.Of(php.MaskInt)), ptr(php.Of(php.MaskString|php.MaskInt)))
	if !wider.Subsumes(it) {
		t.Error("iterators are not covariant in what they yield")
	}
}

// TestNestingComposes. `array<array<int>>` is the case that separates a real
// type from a one-level hack, and PHP code is full of it.
func TestNestingComposes(t *testing.T) {
	inner := php.ArrayOf(nil, ptr(php.Of(php.MaskInt)))
	outer := php.ArrayOf(nil, ptr(inner))
	widerInner := php.ArrayOf(nil, ptr(php.Of(php.MaskNumeric)))
	widerOuter := php.ArrayOf(nil, ptr(widerInner))

	if !widerOuter.Subsumes(outer) {
		t.Error("array<array<int>> is not an array<array<numeric>>; covariance must nest")
	}
	if outer.Subsumes(widerOuter) {
		t.Error("array<array<numeric>> was accepted as array<array<int>>")
	}
	if got := outer.String(); got != "array<array<int>>" {
		t.Errorf("spelled %q, want array<array<int>>", got)
	}
}

// TestUnionWidensElements, which is what an append does.
func TestUnionWidensElements(t *testing.T) {
	ints := php.ArrayOf(nil, ptr(php.Of(php.MaskInt)))
	strs := php.ArrayOf(nil, ptr(php.Of(php.MaskString)))
	u := php.Union(ints, strs)
	if u.Elem == nil || u.Elem.Mask != php.MaskInt|php.MaskString {
		t.Errorf("union of array<int> and array<string> is %s", u)
	}
	if !u.Subsumes(ints) || !u.Subsumes(strs) {
		t.Error("the union does not accept what it was made from")
	}
}
