package php

import "strings"

// Generic types: an array of what, an iterator over what.
//
// # Why a mask is not enough
//
// `TypeMask` says a value may be an array. It cannot say an array *of ints*, so
// reading an element yields `MaskAny` and everything downstream of a container
// is untyped -- which is most of a PHP program. `Type` carries the element type
// alongside the mask, and the container intrinsics thread it: `array.new` makes
// an array of nothing, `array.append` widens the element type, `dim.get`
// answers with it, `iter.new` carries it into the iterator and `iter.current`
// hands it back.
//
// # Unknown is the top, not the bottom
//
// A nil element type means **unknown**, and unknown must behave like "anything"
// rather than "nothing". An array we know nothing about can hold a string, so a
// variant wanting `array<int>` must not accept it. Getting this backwards is
// the whole soundness question here, and it is the reason `Subsumes` refuses
// a nil element against a constrained one rather than treating nil as empty.
//
// # Covariance
//
// `array<int>` is an `array<numeric>` is an `array<any>`: a container is a
// subtype of the same container over a wider element type. That is what makes
// a variant declared over `array<numeric>` usable on an `array<int>`, and it is
// the only variance direction that is sound for a *read*. It is **not** sound
// for a write -- appending a string to an `array<int>` widens it -- which is
// why `array.append` computes a union rather than checking containment.
type Type struct {
	// Mask is the set of PHP types this value may be.
	Mask TypeMask
	// Key and Elem are what a container holds, or nil for unknown. They are
	// pointers so that "unknown" and "nothing" are distinguishable: the zero
	// Type is unknown-everything, not an array of nothing.
	Key, Elem *Type
	// Backing names the concrete implementation behind an object, where the
	// compiler knows it and PHP's type system does not. `ArrayBacked` is the
	// only value so far, and it exists because `foreach` over an array and
	// `foreach` over a Traversable produce the **same PHP type** -- an object
	// holding a cursor -- with completely different effect contracts. Stepping
	// an array cannot run user code and cannot fail; `Iterator::next()` is a
	// method call that can print, throw and re-enter.
	//
	// It is deliberately **not** a class name. Both cursors are objects of the
	// same unspellable internal class at run time, so nothing at run time can
	// tell them apart. This records what the compiler proved about the
	// *subject*, which is a fact about the program rather than about the value.
	//
	// Empty means unknown, and unknown is the top here as it is for the
	// parameters: a variant constrained to array-backed must not accept an
	// iterator nobody has proved anything about, or a foreach over a
	// Traversable would skip the user code it owes.
	Backing string
}

// ArrayBacked marks an iterator whose subject was proved to be an array.
const ArrayBacked = "array"

// Of is a type that is just a mask, with nothing known about any elements.
func Of(m TypeMask) Type { return Type{Mask: m} }

// ArrayOf is an array with known key and element types. A nil argument means
// that half is unknown.
func ArrayOf(key, elem *Type) Type { return Type{Mask: MaskArray, Key: key, Elem: elem} }

// IterOf is an iterator yielding known key and element types. An iterator is an
// object in the value model -- its cursor lives in one -- so the mask says
// object and the parameters say what stepping it produces.
func IterOf(key, elem *Type) Type { return Type{Mask: MaskObject, Key: key, Elem: elem} }

// IterOfArray is an iterator the compiler proved is stepping an array, so no
// step of it runs user code. See Type.Backing.
func IterOfArray(key, elem *Type) Type {
	return Type{Mask: MaskObject, Backing: ArrayBacked, Key: key, Elem: elem}
}

// Known reports whether anything at all is known: the zero Type is not a claim.
func (t Type) Known() bool { return t.Mask != 0 }

// Subsumes reports whether every value of `other` is a value of `t`.
//
// The mask half is ordinary containment. The element half is **covariant**: a
// constraint on elements is satisfied only by a container whose elements are
// known and contained. An unknown element type satisfies nothing but an
// unconstrained parameter, because unknown is the top.
func (t Type) Subsumes(other Type) bool {
	if other.Mask&^t.Mask != 0 {
		return false
	}
	if t.Backing != "" && other.Backing != t.Backing {
		return false
	}
	return subsumesParam(t.Key, other.Key) && subsumesParam(t.Elem, other.Elem)
}

func subsumesParam(want, got *Type) bool {
	if want == nil {
		// No constraint: anything satisfies it, including unknown.
		return true
	}
	if got == nil {
		return false
	}
	return want.Subsumes(*got)
}

// Union is the join: a value that may be either. Unknown element types absorb,
// because the union of "int" and "anything" is "anything".
//
// **The empty mask is the identity**, not a container of nothing. A join needs
// an identity element and `Type{}` is it -- an operand the analysis has not
// computed yet must contribute nothing, or a loop-carried type can never settle
// on anything narrower than `mixed`. Without this the union of `array<int>`
// with a not-yet-computed value dropped the element type, because the zero
// Type's nil parameters read as "unknown" and unknown absorbs.
func Union(a, b Type) Type {
	if a.Mask == 0 {
		return b
	}
	if b.Mask == 0 {
		return a
	}
	backing := a.Backing
	if a.Backing != b.Backing {
		// Two differently-backed iterators joining is an iterator nothing is
		// known about, exactly as two element types join to their union.
		backing = ""
	}
	return Type{
		Mask:    a.Mask | b.Mask,
		Key:     unionParam(a.Key, b.Key),
		Elem:    unionParam(a.Elem, b.Elem),
		Backing: backing,
	}
}

func unionParam(a, b *Type) *Type {
	if a == nil || b == nil {
		return nil
	}
	u := Union(*a, *b)
	return &u
}

// WithElem is `t` with its element type replaced, for threading a container's
// contents through an operation that preserves everything else.
func (t Type) WithElem(key, elem *Type) Type {
	t.Key, t.Elem = key, elem
	return t
}

// String renders a type the way the IR and a diagnostic should spell it:
// `array<string, int>`, `array<int>` when the key says nothing, `int|string`
// for a bare mask.
func (t Type) String() string {
	name := t.Mask.String()
	if t.Backing != "" {
		name += "[" + t.Backing + "]"
	}
	if t.Key == nil && t.Elem == nil {
		return name
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteString("<")
	if t.Key != nil {
		b.WriteString(t.Key.String())
		b.WriteString(", ")
	}
	if t.Elem != nil {
		b.WriteString(t.Elem.String())
	} else {
		b.WriteString("?")
	}
	b.WriteString(">")
	return b.String()
}

// String names a mask the way PHP spells a union type, so a generic type reads
// as `array<string, int|float>` rather than as a number.
func (m TypeMask) String() string {
	if m == 0 {
		return "never"
	}
	if m&MaskAny == MaskAny {
		return "mixed"
	}
	var parts []string
	for _, named := range []struct {
		bit  TypeMask
		name string
	}{
		{MaskNull, "null"}, {MaskBool, "bool"}, {MaskInt, "int"},
		{MaskFloat, "float"}, {MaskString, "string"}, {MaskArray, "array"},
		{MaskObject, "object"}, {MaskResource, "resource"}, {MaskRef, "ref"},
	} {
		if m&named.bit != 0 {
			parts = append(parts, named.name)
		}
	}
	return strings.Join(parts, "|")
}
