package php

import (
	"io"
	"testing"
)

// gcCase builds a fresh registry for one question to CollectWouldBeObserved.
func gcCase(t *testing.T) *Ctx {
	t.Helper()
	registryOn, objectRegistry, trackedObjectBirths = true, nil, 0
	t.Cleanup(func() { registryOn, objectRegistry, trackedObjectBirths = false, nil, 0 })
	return NewCtx(io.Discard)
}

func rootsOf(vals ...Value) func(func(Value)) {
	return func(visit func(Value)) {
		for _, v := range vals {
			visit(v)
		}
	}
}

func withDtor(class string) func(*Object) bool {
	return func(o *Object) bool { return o.ClassName == class }
}

// opaqueInternal is state the walk cannot see into.
type opaqueInternal struct{ held Value }

func TestCollectRefusesWhatWouldBeObserved(t *testing.T) {
	t.Run("unreached object with a destructor", func(t *testing.T) {
		c := gcCase(t)
		a, b := NewObject("D", 1), NewObject("D", 2)
		a.Props.Set(StringKey("o"), Obj(b))
		b.Props.Set(StringKey("o"), Obj(a))
		if got, _ := CollectWouldBeObserved(c, CycleQuery{HasDestructor: withDtor("D")}); !got {
			t.Fatal("a cycle of objects with destructors that nothing reaches must refuse")
		}
	})
	t.Run("destructor already run", func(t *testing.T) {
		c := gcCase(t)
		a := NewObject("D", 1)
		c.MarkDestructed(a)
		if got, _ := CollectWouldBeObserved(c, CycleQuery{HasDestructor: withDtor("D")}); got {
			t.Fatal("an object whose destructor ran has nothing left to run")
		}
	})
	t.Run("count read", func(t *testing.T) {
		c := gcCase(t)
		NewObject("Plain", 1)
		if got, _ := CollectWouldBeObserved(c, CycleQuery{ResultUsed: true}); !got {
			t.Fatal("a read count with an unreached object must refuse")
		}
	})
	t.Run("count read with references", func(t *testing.T) {
		c := gcCase(t)
		if got, _ := CollectWouldBeObserved(c, CycleQuery{ResultUsed: true, ArrayRefs: true}); !got {
			t.Fatal("a read count in a program that makes references must refuse: array cycles are invisible")
		}
	})
	t.Run("handles printed", func(t *testing.T) {
		c := gcCase(t)
		NewObject("Plain", 1)
		if got, _ := CollectWouldBeObserved(c, CycleQuery{HandlesObserved: true}); !got {
			t.Fatal("freeing an unreached object would renumber printed handles")
		}
	})
	t.Run("weakly held", func(t *testing.T) {
		c := gcCase(t)
		k := NewObject("K", 1)
		k.holdWeakly(&WeakMapStore{})
		if got, _ := CollectWouldBeObserved(c, CycleQuery{}); !got {
			t.Fatal("an unreached weakly held object would empty its weak containers")
		}
	})
	t.Run("generator", func(t *testing.T) {
		c := gcCase(t)
		g := NewObject("Generator", 1)
		g.Internal = &Generator{}
		if got, _ := CollectWouldBeObserved(c, CycleQuery{}); !got {
			t.Fatal("an unreached generator would be closed")
		}
	})
	t.Run("held only by an opaque internal", func(t *testing.T) {
		c := gcCase(t)
		inner := NewObject("D", 2)
		outer := NewObject("Opaque", 1)
		outer.Internal = &opaqueInternal{held: Obj(inner)}
		if got, _ := CollectWouldBeObserved(c, CycleQuery{Roots: rootsOf(Obj(outer)), HasDestructor: withDtor("D")}); !got {
			t.Fatal("what an opaque object holds may be garbage; must refuse")
		}
	})
	t.Run("ephemeron value referring to its own key", func(t *testing.T) {
		c := gcCase(t)
		store := &WeakMapStore{}
		m := NewObject("WeakMap", 1)
		m.Internal = store
		k := NewObject("D", 2)
		arr := NewArray()
		arr.Append(Obj(k))
		store.entries = append(store.entries, WeakEntry{key: k, value: Arr(arr)})
		if got, _ := CollectWouldBeObserved(c, CycleQuery{Roots: rootsOf(Obj(m)), HasDestructor: withDtor("D")}); !got {
			t.Fatal("a WeakMap value does not keep its own key alive")
		}
	})
}

func TestCollectKeepsWhatIsReached(t *testing.T) {
	t.Run("reachable cycle", func(t *testing.T) {
		c := gcCase(t)
		a, b := NewObject("D", 1), NewObject("D", 2)
		a.Props.Set(StringKey("o"), Obj(b))
		b.Props.Set(StringKey("o"), Obj(a))
		q := CycleQuery{Roots: rootsOf(Obj(a)), HasDestructor: withDtor("D"), ResultUsed: true, HandlesObserved: true}
		if got, why := CollectWouldBeObserved(c, q); got {
			t.Fatalf("a reached cycle is not garbage: %s", why)
		}
	})
	t.Run("freed objects are not live", func(t *testing.T) {
		c := gcCase(t)
		a := NewObject("D", 1)
		a.TakeIDRelease()
		if got, _ := CollectWouldBeObserved(c, CycleQuery{ResultUsed: true, HasDestructor: withDtor("D")}); got {
			t.Fatal("a freed object is not there to collect")
		}
	})
	t.Run("unreached object nobody would notice", func(t *testing.T) {
		c := gcCase(t)
		NewObject("Plain", 1)
		if got, _ := CollectWouldBeObserved(c, CycleQuery{HasDestructor: withDtor("D")}); got {
			t.Fatal("no destructor, no weak holder, no count read, no handles printed: nothing to observe")
		}
	})
	t.Run("ephemeron with a live key", func(t *testing.T) {
		c := gcCase(t)
		store := &WeakMapStore{}
		m := NewObject("WeakMap", 1)
		m.Internal = store
		k, v := NewObject("D", 2), NewObject("D", 3)
		store.entries = append(store.entries, WeakEntry{key: k, value: Obj(v)})
		q := CycleQuery{Roots: rootsOf(Obj(m), Obj(k)), HasDestructor: withDtor("D")}
		if got, why := CollectWouldBeObserved(c, q); got {
			t.Fatalf("a live key keeps its value: %s", why)
		}
	})
	t.Run("held by the request's handlers", func(t *testing.T) {
		c := gcCase(t)
		e, x, a := NewObject("D", 1), NewObject("D", 2), NewObject("D", 3)
		c.PushErrorHandler(Obj(e))
		c.PushExceptionHandler(Obj(x))
		c.autoloaders = append(c.autoloaders, Obj(a))
		if got, why := CollectWouldBeObserved(c, CycleQuery{HasDestructor: withDtor("D")}); got {
			t.Fatalf("an installed handler is held: %s", why)
		}
	})
	t.Run("running fiber", func(t *testing.T) {
		c := gcCase(t)
		f := NewObject("Fiber", 1)
		f.Internal = &FiberHandle{}
		held := NewObject("D", 2)
		f.Props.Set(StringKey("x"), Obj(held))
		leave := c.EnterFiber(f)
		defer leave()
		if got, why := CollectWouldBeObserved(c, CycleQuery{HasDestructor: withDtor("D")}); got {
			t.Fatalf("a running fiber is on the stack: %s", why)
		}
	})
}
