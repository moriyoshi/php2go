package php

import "testing"

func TestCollectPlainCycles(t *testing.T) {
	c := gcCase(t)
	c.SetReleaseHandle(func(o *Object) { o.TakeIDRelease() })
	q := CycleQuery{Roots: rootsOf(), HasDestructor: func(*Object) bool { return false }}
	dead := NewObject("Node", 1)
	a := NewObject("Node", 2)
	b := NewObject("Node", 3)
	a.Props.Set(StringKey("next"), Obj(b))
	b.Props.Set(StringKey("next"), Obj(a))
	b.Props.Set(StringKey("tail"), Obj(dead))
	count, err := CollectPlainCycles(c, q)
	if err != nil || count != 3 {
		t.Fatalf("first collection = %d, %v; want 3", count, err)
	}
	count, err = CollectPlainCycles(c, q)
	if err != nil || count != 0 {
		t.Fatalf("second collection = %d, %v; want 0", count, err)
	}
}

func TestCollectPlainCyclesKeepsLiveCycle(t *testing.T) {
	c := gcCase(t)
	a := NewObject("Node", 1)
	a.Props.Set(StringKey("self"), Obj(a))
	count, err := CollectPlainCycles(c, CycleQuery{
		Roots: rootsOf(Obj(a)), HasDestructor: func(*Object) bool { return false },
	})
	if err != nil || count != 0 {
		t.Fatalf("live cycle = %d, %v; want 0", count, err)
	}
}

func TestCollectPlainCyclesWithDynamicArray(t *testing.T) {
	c := gcCase(t)
	c.SetReleaseHandle(func(o *Object) { o.TakeIDRelease() })
	o := NewObject("Node", 1)
	a := NewArray()
	a.Append(Obj(o))
	o.Props.Set(StringKey("array"), Arr(a))
	count, err := CollectPlainCycles(c, CycleQuery{
		Roots: rootsOf(), HasDestructor: func(*Object) bool { return false }, ResultUsed: true,
	})
	if err != nil || count != 2 {
		t.Fatalf("object-array cycle = %d, %v; want 2", count, err)
	}
}

func TestCollectPlainCyclesKeepsSharedArrayBody(t *testing.T) {
	c := gcCase(t)
	c.SetReleaseHandle(func(o *Object) { o.TakeIDRelease() })
	o := NewObject("Node", 1)
	o.Props.Set(StringKey("self"), Obj(o))
	a := NewArray()
	a.Append(Int(5))
	o.Props.Set(StringKey("array"), Arr(a.Share()))
	count, err := CollectPlainCycles(c, CycleQuery{
		Roots: rootsOf(Arr(a)), HasDestructor: func(*Object) bool { return false }, ResultUsed: true,
	})
	if err != nil || count != 1 {
		t.Fatalf("cycle with live shared array body = %d, %v; want 1", count, err)
	}
}

func TestCollectPlainCyclesRefusesUncertainArrayTemporary(t *testing.T) {
	c := gcCase(t)
	a := NewArray()
	o := NewObject("Node", 1)
	a.Append(Obj(o))
	o.Props.Set(StringKey("array"), Arr(a))
	_, err := CollectPlainCycles(c, CycleQuery{
		Roots: rootsOf(), TempRoots: rootsOf(Arr(a)),
		HasDestructor: func(*Object) bool { return false }, ResultUsed: true,
	})
	if err == nil || o.idReleased {
		t.Fatal("an array compiler temporary outside definite roots must refuse")
	}
}

func TestCollectPlainCyclesRefusesOpaqueRoot(t *testing.T) {
	c := gcCase(t)
	a := NewObject("Node", 1)
	a.Props.Set(StringKey("self"), Obj(a))
	root := NewObject("Opaque", 2)
	root.Internal = &opaqueInternal{held: Obj(a)}
	_, err := CollectPlainCycles(c, CycleQuery{
		Roots: rootsOf(Obj(root)), HasDestructor: func(*Object) bool { return false }, ResultUsed: true,
	})
	if err == nil || a.idReleased {
		t.Fatal("opaque root must refuse before collecting")
	}
}

func TestCollectPlainCyclesRefusesUncertainTemporary(t *testing.T) {
	c := gcCase(t)
	a := NewObject("Node", 1)
	a.Props.Set(StringKey("self"), Obj(a))
	_, err := CollectPlainCycles(c, CycleQuery{
		Roots: rootsOf(), TempRoots: rootsOf(Obj(a)),
		HasDestructor: func(*Object) bool { return false }, ResultUsed: true,
	})
	if err == nil || a.idReleased {
		t.Fatal("a compiler temporary outside definite roots must refuse")
	}
}

func TestCollectPlainCyclesRefusesAfterManyAllocations(t *testing.T) {
	c := gcCase(t)
	a := NewObject("Node", 1)
	a.Props.Set(StringKey("self"), Obj(a))
	trackedObjectBirths = 4096
	_, err := CollectPlainCycles(c, CycleQuery{
		Roots: rootsOf(), HasDestructor: func(*Object) bool { return false }, ResultUsed: true,
	})
	if err == nil || a.idReleased {
		t.Fatal("automatic collection may already have changed the count")
	}
}
