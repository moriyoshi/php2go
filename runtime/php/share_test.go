package php_test

import (
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

func intList(vals ...int64) *php.Array {
	a := php.NewArray()
	for _, v := range vals {
		a.Append(php.Int(v))
	}
	return a
}

func sharedInts(a *php.Array) []int64 {
	var out []int64
	a.Each(func(_ php.Key, v php.Value) bool {
		out = append(out, v.RawInt())
		return true
	})
	return out
}

func sameInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Every way of changing an array, applied to one holder of a shared body, must
// leave the other holder as it was. A mutator that forgets to separate is the
// only way the two can see each other's writes, so each one is listed.
func TestShareSeparatesOnEveryMutator(t *testing.T) {
	for name, write := range map[string]func(*php.Array){
		"Set":         func(a *php.Array) { a.Set(php.IntKey(0), php.Int(9)) },
		"Append":      func(a *php.Array) { a.Append(php.Int(9)) },
		"Unset":       func(a *php.Array) { a.Unset(php.IntKey(1)) },
		"Bind":        func(a *php.Array) { a.Bind(php.IntKey(0), php.Int(9)) },
		"Rebind":      func(a *php.Array) { a.Rebind(php.IntKey(0), php.Int(9)) },
		"RefAt":       func(a *php.Array) { a.RefAt(php.IntKey(0)).Set(php.Int(9)) },
		"Rekey":       func(a *php.Array) { a.Rekey(php.IntKey(0), php.IntKey(7)) },
		"PopLast":     func(a *php.Array) { a.PopLast() },
		"ShiftFirst":  func(a *php.Array) { a.ShiftFirst() },
		"Renumber":    func(a *php.Array) { a.Unset(php.IntKey(0)); a.Renumber() },
		"Prepend":     func(a *php.Array) { a.Prepend([]php.Value{php.Int(9)}) },
		"Splice":      func(a *php.Array) { a.Splice(0, 1, []php.Value{php.Int(9)}, true) },
		"MovePointer": func(a *php.Array) { a.MovePointer(1) },
	} {
		t.Run(name, func(t *testing.T) {
			a := intList(1, 2, 3)
			b := a.Share()
			write(b)
			if got := sharedInts(a); !sameInts(got, []int64{1, 2, 3}) {
				t.Fatalf("the other holder changed: %v", got)
			}
			// And the other way round, from the holder that kept the body.
			c := intList(1, 2, 3)
			d := c.Share()
			write(c)
			if got := sharedInts(d); !sameInts(got, []int64{1, 2, 3}) {
				t.Fatalf("the sharing holder changed: %v", got)
			}
		})
	}
}

// A write into a nested array descends through the outer one's RefAt, which
// separates the outer first, so the nested array the writer reaches is its own.
func TestShareSeparatesNestedWrites(t *testing.T) {
	outer := php.NewArray()
	outer.Set(php.IntKey(0), php.Arr(intList(1)))
	other := outer.Share()

	box := outer.RefAt(php.IntKey(0))
	box.Slot().Array().Append(php.Int(2))

	inner, _ := other.Get(php.IntKey(0))
	if got := sharedInts(inner.Array()); !sameInts(got, []int64{1}) {
		t.Fatalf("a write into the writer's nested array reached the other holder: %v", got)
	}
	mine, _ := outer.Get(php.IntKey(0))
	if got := sharedInts(mine.Array()); !sameInts(got, []int64{1, 2}) {
		t.Fatalf("the writer's nested array did not change: %v", got)
	}
}

// References follow PHP: decided when the body separates, not when it is
// shared. One still held by another name survives, so both arrays see a write
// through it; one only the array holds is flattened into the writer's copy.
func TestShareDecidesReferencesAtSeparation(t *testing.T) {
	t.Run("still held elsewhere", func(t *testing.T) {
		a := intList(1, 2)
		// `$r = &$a[0]`: the element's box, bound by an explicit `&`, which is
		// what php.Shared records.
		var r php.Value
		php.BindRef(&r, refSlot(a, 0))
		php.Shared(r)
		b := a.Share()
		a.Set(php.IntKey(0), php.Int(7))
		if got, _ := b.Get(php.IntKey(0)); got.RawInt() != 7 {
			t.Fatalf("a reference two names hold must survive the copy: got %d", got.RawInt())
		}
	})
	t.Run("held only by the array", func(t *testing.T) {
		a := intList(1, 2)
		a.RefAt(php.IntKey(0)) // a box nothing else binds
		b := a.Share()
		a.Set(php.IntKey(0), php.Int(7))
		if got, _ := b.Get(php.IntKey(0)); got.RawInt() != 1 {
			t.Fatalf("a box only the array held is flattened in the writer's copy: got %d", got.RawInt())
		}
	})
}

// refSlot is a pointer to an element's storage, boxed, for BindRef.
func refSlot(a *php.Array, i int64) *php.Value {
	v := php.MakeRef(a.RefAt(php.IntKey(i)))
	return &v
}

// The internal pointer belongs to the holder.
func TestShareKeepsPointersApart(t *testing.T) {
	a := intList(1, 2, 3)
	a.MovePointer(1)
	b := a.Share()
	b.MovePointer(1)
	if _, v, _ := a.Pointed(); v.RawInt() != 2 {
		t.Fatalf("the first holder's pointer moved with the second's: at %d", v.RawInt())
	}
	if _, v, _ := b.Pointed(); v.RawInt() != 3 {
		t.Fatalf("the shared copy starts where the original pointed: at %d", v.RawInt())
	}
}

// A holder the compiler proved dead hands its share back, and the holder that
// remains then writes in place -- no copy -- while a share still held keeps
// the separation.
func TestReleaseSharedRestoresInPlaceWrites(t *testing.T) {
	a := intList(1, 2)
	before := a.Identity()
	b := a.Share()
	php.ReleaseShared(php.Arr(b))
	a.Append(php.Int(3))
	if a.Identity() != before {
		t.Fatalf("a released share still made the writer copy")
	}

	c := intList(1, 2)
	d := c.Share()
	e := c.Share()
	php.ReleaseShared(php.Arr(d))
	c.Append(php.Int(3))
	if got := sharedInts(e); !sameInts(got, []int64{1, 2}) {
		t.Fatalf("a share still held must keep the separation: %v", got)
	}
	// Releasing more than was shared must not take the count below zero.
	f := intList(1)
	php.ReleaseShared(php.Arr(f))
	php.ReleaseShared(php.Arr(f))
	g := f.Share()
	f.Append(php.Int(2))
	if got := sharedInts(g); !sameInts(got, []int64{1}) {
		t.Fatalf("an over-release let a later share see a write: %v", got)
	}
}
