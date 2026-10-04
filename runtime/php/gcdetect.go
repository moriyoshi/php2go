package php

// Deciding whether gc_collect_cycles would do anything observable.
//
// Compiled code has only a bounded top-level object and dynamic-array collector
// (gccollect.go). At other sites it must not answer `0` and carry on
// when the engine would have collected something the program can see: run a
// destructor, empty a weak reference, close a generator or a fiber, hand back
// a non-zero count, or free a handle number a later object would take. This
// file finds out whether that can be the case, and the caller refuses
// (unsupported) when it can.
//
// The walk is **one-sided on purpose**. It marks what the known roots reach;
// a root it does not know about (a local of a function on the stack, a
// temporary, a value the context holds) can only make a live object look
// unreachable, which turns into a refusal -- never into a wrong `0`. What it
// must never do is treat a holder PHP has already let go of as a root, which
// is why compiler temporaries are not roots.
//
// Every object the walk does not reach counts as possibly collectable --
// see CollectWouldBeObserved for why cycles in php2go's own heap cannot be
// told from the engine's.

var (
	objectRegistry      []*Object
	registryOn          bool
	trackedObjectBirths uint64
)

// TrackObjects starts recording every object made, for a program that calls
// gc_collect_cycles. Others pay nothing.
func TrackObjects() {
	registryOn = true
	objectRegistry = nil
	trackedObjectBirths = 0
}

// RegisterObject records an object made outside NewObject and CloneObject --
// a compiled class's struct-backed instance.
func RegisterObject(o *Object) {
	if registryOn && o != nil {
		objectRegistry = append(objectRegistry, o)
		trackedObjectBirths++
	}
}

// EachRegistered calls fn for every recorded object not yet freed.
func EachRegistered(fn func(*Object)) {
	for _, o := range objectRegistry {
		if o != nil && !o.idReleased {
			fn(o)
		}
	}
}

// GCVisitor is an object's internal state saying what it holds, and whether
// it could say all of it. An Internal that does not implement it, or answers
// false, is **opaque**: the walk cannot tell what it keeps alive, so anything
// it might hold is treated as possibly collectable.
type GCVisitor interface {
	GCVisit(visit func(Value)) bool
}

// GCVisit shows a generator's frame, arguments and the values it holds
// between steps. A frame or a `yield from` source that cannot show its own
// contents leaves the generator partly opaque.
func (g *Generator) GCVisit(visit func(Value)) bool {
	ok := visitResumable(g.body, visit)
	for _, a := range g.Args {
		visit(a)
	}
	visit(g.suspendedWith)
	visit(g.key)
	visit(g.current)
	visit(g.ret)
	if g.delegate != nil {
		if d, isVisitor := g.delegate.(GCVisitor); !isVisitor || !d.GCVisit(visit) {
			ok = false
		}
	}
	return ok
}

// GCVisit shows a fiber's callable and, once started, its stack.
func (h *FiberHandle) GCVisit(visit func(Value)) bool {
	visit(h.Callable)
	if h.Driver == nil {
		return true
	}
	visit(h.Driver.ret)
	return visitResumable(h.Driver.body, visit)
}

// visitResumable shows a suspended body: a call stack frame by frame, or a
// compiled frame that shows its own fields.
func visitResumable(r Resumable, visit func(Value)) bool {
	switch b := r.(type) {
	case nil:
		return true
	case *callStack:
		ok := visitResumable(b.root, visit)
		for _, f := range b.inner {
			if !visitResumable(f.body, visit) {
				ok = false
			}
		}
		return ok
	case GCVisitor:
		return b.GCVisit(visit)
	}
	return false
}

// CycleQuery is what the call site knows that the runtime does not.
type CycleQuery struct {
	// Roots visits every value the program certainly still holds.
	Roots func(visit func(Value))
	// TempRoots visits compiler slots that may retain a value after its PHP
	// lifetime. They are not roots, but collection must refuse if one holds
	// an object outside the definite root set.
	TempRoots func(visit func(Value))
	// HasDestructor answers whether an object's class declares one.
	HasDestructor func(*Object) bool
	// ResultUsed is a call whose count the program reads.
	ResultUsed bool
	// ArrayRefs is a program that makes references, which can close a cycle
	// through arrays alone -- no object on it, so nothing the object walk
	// can see. Its count cannot be vouched for.
	ArrayRefs bool
	// HandlesObserved is a program that can print object handle numbers
	// (var_dump, spl_object_id, ...): freeing garbage would renumber later
	// objects.
	HandlesObserved bool
}

// CollectWouldBeObserved reports whether collecting cycles now could have an
// effect the program observes, and why. False means `0` and doing nothing is
// exactly what the engine's collector would amount to.
func CollectWouldBeObserved(c *Ctx, q CycleQuery) (bool, string) {
	if q.ResultUsed && q.ArrayRefs {
		return true, "gc_collect_cycles count in a program that makes references (no cycle collector)"
	}
	m := &gcMark{seen: map[*Object]bool{}, arrays: map[*Array]bool{}}
	if q.Roots != nil {
		q.Roots(m.value)
	}
	// **A fiber that is running is on the stack**, and so is everything its
	// frames hold: gc_collect_cycles called from inside one (or from a fiber
	// it started) cannot collect it (fibers/gh10496-002). A suspended fiber
	// is not a root -- the engine collects one nothing reaches (gh9735).
	for _, o := range c.enteredFibers {
		m.object(o)
	}
	// The handlers the context holds are held for the rest of the request.
	for _, h := range c.errorHandlers {
		m.value(h)
	}
	for _, h := range c.exceptionHandlers {
		m.value(h)
	}
	for _, h := range c.autoloaders {
		m.value(h)
	}
	// **A WeakMap entry is an ephemeron**: its value is kept by the map only
	// while its key is alive. A value that refers back to its own key keeps
	// nothing alive on its own (weakrefs/gh10043), so values are reached only
	// once their key is, to a fixpoint.
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(m.weakMaps); i++ {
			for _, e := range m.weakMaps[i].entries {
				if e.key != nil && m.seen[e.key] && !m.valued[e.key] {
					if m.valued == nil {
						m.valued = map[*Object]bool{}
					}
					m.valued[e.key] = true
					m.value(e.value)
					changed = true
				}
			}
		}
	}
	// The objects still alive as far as php2go knows, and not reached.
	live := objectRegistry[:0]
	var unreached []*Object
	for _, o := range objectRegistry {
		if o == nil || o.idReleased {
			continue
		}
		live = append(live, o)
		if !m.seen[o] {
			unreached = append(unreached, o)
		}
	}
	objectRegistry = live
	if len(unreached) == 0 {
		return false, ""
	}
	// **Every unreached object counts**, cyclic in php2go's heap or not.
	// php2go's heap is not the engine's: a value the engine keeps on a
	// stack -- an argument pushed before a `yield` (gh9750-002), a
	// temporary -- may be missing here, and with it the edge that closes
	// the engine's cycle. And an object the engine freed by refcount long
	// ago but php2go never released is unreached here too: refusing over
	// it is merely conservative.
	garbage := unreached
	if q.ResultUsed {
		return true, "gc_collect_cycles count with collectable cycles (no cycle collector)"
	}
	if q.HandlesObserved {
		return true, "gc_collect_cycles with collectable cycles in a program that prints object handles (no cycle collector)"
	}
	for _, o := range garbage {
		switch o.Internal.(type) {
		case *Generator, *FiberHandle:
			return true, "gc_collect_cycles with a collectable generator or fiber (no cycle collector)"
		}
		if o.HeldWeakly() {
			return true, "gc_collect_cycles with a weakly referenced collectable object (no cycle collector)"
		}
		if !c.Destructed(o) && q.HasDestructor != nil && q.HasDestructor(o) {
			return true, "gc_collect_cycles with a collectable object that has a destructor (no cycle collector)"
		}
	}
	return false, ""
}

type gcMark struct {
	seen     map[*Object]bool
	arrays   map[*Array]bool
	opaque   bool
	resource bool
	// weakMaps are the reached maps whose entries wait on their keys; valued
	// records the keys whose value has been walked.
	weakMaps []*WeakMapStore
	valued   map[*Object]bool
}

func (m *gcMark) value(v Value) {
	v = v.Deref()
	switch v.Kind() {
	case KindObject:
		m.object(v.Object())
	case KindArray:
		m.array(v.Array())
	case KindResource:
		m.resource = true
	}
}

func (m *gcMark) array(a *Array) {
	if a == nil || m.arrays[a] {
		return
	}
	m.arrays[a] = true
	a.Each(func(_ Key, item Value) bool {
		m.value(item)
		return true
	})
}

func (m *gcMark) object(o *Object) {
	if o == nil || m.seen[o] {
		return
	}
	m.seen[o] = true
	if w, ok := o.Internal.(*WeakMapStore); ok {
		m.weakMaps = append(m.weakMaps, w)
	}
	if !visitObject(o, m.value) {
		m.opaque = true
	}
}

// visitObject visits what an object holds, and reports false when part of it
// is opaque.
func visitObject(o *Object, visit func(Value)) bool {
	if o.Props != nil {
		visit(Arr(o.Props))
	}
	for _, s := range o.Slots {
		visit(s)
	}
	if o.Lazy != nil {
		visit(o.Lazy.Initializer)
		if o.Lazy.Instance != nil {
			visit(Obj(o.Lazy.Instance))
		}
	}
	switch in := o.Internal.(type) {
	case nil:
		return true
	case GCVisitor:
		return in.GCVisit(visit)
	case *ArrayStore:
		if in.items != nil {
			visit(Arr(in.items))
		}
		return true
	case *WeakMapStore:
		// Keys are weak, and values wait on their keys (the ephemeron
		// fixpoint in CollectWouldBeObserved).
		return true
	case *WeakRefStore:
		// Weak: holds nothing alive.
		return true
	case *SensitiveValue:
		visit(in.value)
		return true
	}
	return false
}

// SetSelf records the object a generator is the Internal of.
func (g *Generator) SetSelf(o *Object) { g.self = o }

// GCVisit shows a `yield from` over a generator: the generator object.
func (d *generatorDelegate) GCVisit(visit func(Value)) bool {
	if d.g == nil {
		return true
	}
	if d.g.self != nil {
		visit(Obj(d.g.self))
		return true
	}
	return d.g.GCVisit(visit)
}

// GCVisit shows a `yield from` over an array: its remaining values.
func (d *arrayDelegate) GCVisit(visit func(Value)) bool {
	for _, v := range d.values {
		visit(v)
	}
	return true
}

// GCVisit shows a `yield from` over a Traversable, when the traversal can
// say what it holds.
func (d *traversableDelegate) GCVisit(visit func(Value)) bool {
	if t, ok := d.trav.(GCVisitor); ok {
		return t.GCVisit(visit)
	}
	return false
}
