package interp

import (
	"github.com/moriyoshi/php2go/runtime/php"
)

// What gc_collect_cycles() reports.
//
// The engine answers with the number of values its cycle collector freed, and
// that is a narrower set than "everything the program can no longer reach". A
// value PHP's refcount would already have released never reaches the collector
// at all, so it is not counted: `$a = new stdClass; $a = null;` collects
// nothing even though an object died. What the collector frees is a cycle --
// and, once it has broken one, everything that was hanging off it.
//
// Probed against the engine rather than derived:
//
//	$b->s = $b                                  1  the object is on a cycle
//	$c->o = $d; $d->o = $c                      2  both are
//	$e->o = $f                                  0  neither is
//	$g[] =& $g                                  1  an array can be on one too
//	$c->o = $d; $d->o = $c; $c->extra = new …   3  what hangs off one goes too
//	$g2->o = $e (into a cycle, from outside)    0  pointing at one is not being on one
//
// So: the unreachable values that lie on a cycle, plus the unreachable values
// reachable from those. Everything else unreachable is what a refcount would
// have taken care of, and the engine has already counted it as nothing.
//
// The one place php2go cannot follow is an array literal the compiler folded to
// a constant. The engine marks those immutable and leaves them out of the
// collector entirely; php2go has no such mark, so `$x->a = [1, 2, [3]]` hanging
// off a cycle counts its arrays where the engine does not.

// node is a value the collector can free: an object or an array. References are
// not nodes -- the engine does not count them, and a cycle through one is
// attributed to the array or object at its end.
type node any

// cycleCount reports how many values the engine's cycle collector would have
// freed. reachable is the marking already done for the collection itself, so
// the walk here only has to cover what that did not reach.
func (u *Unit) cycleCount(reachable *marker) int {
	dead := u.deadGraph(reachable)
	if len(dead) == 0 {
		return 0
	}
	onCycle := cyclicNodes(dead)
	if len(onCycle) == 0 {
		return 0
	}
	// Breaking a cycle frees what was hanging off it, and the engine counts
	// those too.
	freed := map[node]bool{}
	var reach func(n node)
	reach = func(n node) {
		if freed[n] {
			return
		}
		freed[n] = true
		for _, next := range dead[n] {
			reach(next)
		}
	}
	for n := range onCycle {
		reach(n)
	}
	return len(freed)
}

// deadGraph builds the part of the value graph the program can no longer reach,
// as an adjacency list over objects and arrays.
func (u *Unit) deadGraph(reachable *marker) map[node][]node {
	dead := map[node][]node{}
	var walkValue func(v php.Value) (node, bool)
	var walkObject func(o *php.Object)
	var walkArray func(a *php.Array)

	edges := func(from node, v php.Value) {
		if to, ok := walkValue(v); ok {
			dead[from] = append(dead[from], to)
		}
	}
	walkValue = func(v php.Value) (node, bool) {
		v = v.Deref()
		switch v.Kind() {
		case php.KindObject:
			if o := v.Object(); o != nil && !reachable.seen[o] {
				walkObject(o)
				return node(o), true
			}
		case php.KindArray:
			if a := v.Array(); a != nil && !reachable.arrays[a] {
				walkArray(a)
				return node(a), true
			}
		}
		return nil, false
	}
	walkObject = func(o *php.Object) {
		if _, done := dead[o]; done {
			return
		}
		dead[o] = nil
		// The property table itself is not a value the engine counts -- an
		// object's properties are not a separate array in the engine -- so the
		// edges run from the object straight to what its properties hold.
		o.Props.Each(func(_ php.Key, v php.Value) bool {
			edges(node(o), v)
			return true
		})
		for _, v := range u.internalValues(o) {
			edges(node(o), v)
		}
	}
	walkArray = func(a *php.Array) {
		if _, done := dead[a]; done {
			return
		}
		dead[a] = nil
		a.Each(func(_ php.Key, v php.Value) bool {
			edges(node(a), v)
			return true
		})
	}

	for _, object := range u.objects {
		if object == nil || reachable.seen[object] || u.destructed[object] {
			continue
		}
		walkObject(object)
	}
	// A cycle made only of arrays needs a reference to exist at all, so the
	// arrays that have had one bound into them are where to start looking for
	// one. Nothing else can put an array in a cycle without an object.
	for _, host := range u.refHosts {
		if host == nil || reachable.arrays[host] {
			continue
		}
		walkArray(host)
	}
	return dead
}

// internalValues is what an object of an engine-provided class holds that its
// property table does not: a closure's bound variables, a generator's frame.
// Only the shapes that can close a cycle are listed; a value that cannot point
// back at an object is not worth walking.
func (u *Unit) internalValues(o *php.Object) []php.Value {
	switch held := o.Internal.(type) {
	case *closure:
		values := make([]php.Value, 0, len(held.bound)+1)
		for _, value := range held.bound {
			values = append(values, value)
		}
		if held.this != nil {
			values = append(values, php.Obj(held.this))
		}
		return values
	case *generator:
		values := []php.Value{held.returned, held.sent}
		if held.frame != nil {
			values = append(values, held.frame.slots...)
			if held.frame.this != nil {
				values = append(values, php.Obj(held.frame.this))
			}
		}
		return values
	}
	return nil
}

// cyclicNodes finds the nodes that lie on a cycle: Tarjan's strongly connected
// components, keeping the ones with more than one member and the single ones
// that point at themselves.
func cyclicNodes(graph map[node][]node) map[node]bool {
	type state struct {
		index, low int
		onStack    bool
	}
	states := make(map[node]*state, len(graph))
	var stack []node
	next := 0
	cyclic := map[node]bool{}

	// Iterative, because a dead graph can be as deep as the program's data.
	type step struct {
		at    node
		child int
	}
	for start := range graph {
		if states[start] != nil {
			continue
		}
		states[start] = &state{index: next, low: next, onStack: true}
		next++
		stack = append(stack, start)
		work := []step{{at: start}}
		for len(work) > 0 {
			frame := &work[len(work)-1]
			edges := graph[frame.at]
			if frame.child < len(edges) {
				child := edges[frame.child]
				frame.child++
				known := states[child]
				switch {
				case known == nil:
					states[child] = &state{index: next, low: next, onStack: true}
					next++
					stack = append(stack, child)
					work = append(work, step{at: child})
				case known.onStack:
					if known.index < states[frame.at].low {
						states[frame.at].low = known.index
					}
				}
				continue
			}
			at := frame.at
			work = work[:len(work)-1]
			if len(work) > 0 {
				parent := states[work[len(work)-1].at]
				if states[at].low < parent.low {
					parent.low = states[at].low
				}
			}
			if states[at].low != states[at].index {
				continue
			}
			// The root of a component: everything above it on the stack is in
			// it. A component of one is only a cycle if it points at itself.
			var component []node
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				states[top].onStack = false
				component = append(component, top)
				if top == at {
					break
				}
			}
			if len(component) == 1 {
				selfLoop := false
				for _, child := range graph[at] {
					if child == at {
						selfLoop = true
						break
					}
				}
				if !selfLoop {
					continue
				}
			}
			for _, member := range component {
				cyclic[member] = true
			}
		}
	}
	return cyclic
}

// handle answers the handle the next object should carry: the most recently
// freed one if there is one, and a fresh one otherwise.
func (u *Unit) handle() int {
	if n := len(u.freeHandles); n > 0 {
		id := u.freeHandles[n-1]
		u.freeHandles = u.freeHandles[:n-1]
		return id
	}
	u.nextObjectID++
	return u.nextObjectID
}

// born records an object so that gc_collect_cycles() can tell whether it is
// still reachable. Objects with destructors are kept separately as well, in
// `live`, because *those* drive collection; this list only has to be complete
// when the program asks for a count.
func (u *Unit) born(object *php.Object) *php.Object {
	u.objects = append(u.objects, object)
	return object
}

// forget drops what the program can no longer reach from the lists that only
// exist to answer gc_collect_cycles(). In the engine those values are gone by
// now; keeping them would both count them again and hold the memory.
func (u *Unit) forget(reachable *marker) {
	kept := u.objects[:0]
	for _, object := range u.objects {
		if object != nil && reachable.seen[object] {
			kept = append(kept, object)
		}
	}
	for i := len(kept); i < len(u.objects); i++ {
		u.objects[i] = nil
	}
	u.objects = kept

	hosts := u.refHosts[:0]
	for _, host := range u.refHosts {
		if host != nil && reachable.arrays[host] {
			hosts = append(hosts, host)
		}
	}
	for i := len(hosts); i < len(u.refHosts); i++ {
		u.refHosts[i] = nil
	}
	u.refHosts = hosts
}

// built takes an object off the under-construction list. The list is a stack,
// so the usual case is the last entry; a constructor that unwound past one
// leaves it to be found by name.
func (u *Unit) built(object *php.Object) {
	for i := len(u.building) - 1; i >= 0; i-- {
		if u.building[i] == object {
			u.building = append(u.building[:i], u.building[i+1:]...)
			return
		}
	}
}

// hostsRef records an array a reference has been bound into. It is the only way
// an array can end up in a cycle without an object being in it too, so it is
// where the search for such a cycle starts.
func (u *Unit) hostsRef(array *php.Array) {
	if array != nil {
		u.refHosts = append(u.refHosts, array)
	}
}
