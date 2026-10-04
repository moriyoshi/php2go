package php

// CollectPlainCycles collects the subset of cycles whose effects can be
// proved from a top-level compiled call: plain objects and dynamic arrays
// connected by their values. Anything with an unknown root or a special free path is refused
// before changing the heap. This deliberately leaves the broader collector to
// a later increment.
func CollectPlainCycles(c *Ctx, q CycleQuery) (int, error) {
	if c == nil {
		return 0, Unsupported("gc_collect_cycles without a request context")
	}
	refuseOrZero := func(reason string) (int, error) {
		if observed, _ := CollectWouldBeObserved(c, q); !observed {
			return 0, nil
		}
		return 0, Unsupported("%s", reason)
	}
	if q.Roots == nil || q.HasDestructor == nil {
		return 0, Unsupported("gc_collect_cycles without complete roots or class facts")
	}
	if q.ArrayRefs {
		return refuseOrZero("gc_collect_cycles with array references")
	}
	// A long allocation run may already have filled Zend's possible-root
	// buffer and caused an automatic collection before this explicit call.
	// The registry is not a refcount-event log, so stop well below the
	// observed 10001-root threshold instead of returning a stale count.
	if trackedObjectBirths >= 4096 {
		return refuseOrZero("gc_collect_cycles after many object allocations")
	}
	m := &gcMark{seen: map[*Object]bool{}, arrays: map[*Array]bool{}}
	q.Roots(m.value)
	if !c.gcVisitRoots(m.value) || m.opaque || m.resource {
		return refuseOrZero("gc_collect_cycles with opaque request roots")
	}
	reachableBodies := map[*arrayBody]bool{}
	for a := range m.arrays {
		reachableBodies[a.arrayBody] = true
	}
	if q.TempRoots != nil {
		temps := &gcMark{seen: map[*Object]bool{}, arrays: map[*Array]bool{}}
		q.TempRoots(temps.value)
		if temps.opaque || temps.resource {
			return refuseOrZero("gc_collect_cycles with opaque compiler temporaries")
		}
		for o := range temps.seen {
			if !m.seen[o] && !o.idReleased {
				return refuseOrZero("gc_collect_cycles with a live compiler temporary")
			}
		}
		for a := range temps.arrays {
			if !reachableBodies[a.arrayBody] {
				return refuseOrZero("gc_collect_cycles with a live compiler array temporary")
			}
		}
	}

	// Build only the unreachable graph. An object's property table is not a
	// counted PHP array; an array held by a property is a separate node.
	type gcNode struct {
		object *Object
		array  *arrayBody
	}
	graph := map[gcNode][]gcNode{}
	var unsupported bool
	var walkValue func(gcNode, Value)
	var walkObject func(*Object)
	var walkArray func(*Array)
	walkValue = func(from gcNode, v Value) {
		v = v.Deref()
		switch v.Kind() {
		case KindObject:
			if next := v.Object(); next != nil && !m.seen[next] && !next.idReleased {
				to := gcNode{object: next}
				graph[from] = append(graph[from], to)
				walkObject(next)
			}
		case KindArray:
			if next := v.Array(); next != nil && !reachableBodies[next.arrayBody] {
				to := gcNode{array: next.arrayBody}
				graph[from] = append(graph[from], to)
				walkArray(next)
			}
		case KindResource:
			unsupported = true
		}
	}
	walkObject = func(o *Object) {
		if o == nil || o.idReleased || m.seen[o] {
			return
		}
		from := gcNode{object: o}
		if _, ok := graph[from]; ok {
			return
		}
		graph[from] = nil
		if q.HasDestructor(o) || o.HeldWeakly() || o.Lazy != nil || o.Internal != nil {
			unsupported = true
		}
		if o.Props != nil {
			o.Props.Each(func(_ Key, v Value) bool { walkValue(from, v); return true })
		}
		for _, v := range o.Slots {
			walkValue(from, v)
		}
	}
	walkArray = func(a *Array) {
		if a == nil || reachableBodies[a.arrayBody] {
			return
		}
		from := gcNode{array: a.arrayBody}
		if _, ok := graph[from]; ok {
			return
		}
		graph[from] = nil
		// The root walk protects a live holder by body identity, including
		// aliases made by Share. Folded constant arrays do not enter Zend's root
		// buffer, so their count would differ from a dynamic array's.
		if a.arrayBody == nil || a.openWrites != 0 || a.interned {
			unsupported = true
			return
		}
		a.Each(func(_ Key, v Value) bool { walkValue(from, v); return true })
	}
	EachRegistered(walkObject)
	if unsupported {
		return refuseOrZero("gc_collect_cycles with non-plain unreachable values")
	}
	// Trial deletion: an unreachable object is counted only when it belongs to
	// a cycle, or hangs from one. An acyclic object was already freed by PHP's
	// refcounting and is absent from its collector's return value.
	type state struct {
		index, low int
		onStack    bool
	}
	states := map[gcNode]*state{}
	var stack []gcNode
	var cyclic []gcNode
	nextIndex := 0
	var strongConnect func(gcNode)
	strongConnect = func(o gcNode) {
		states[o] = &state{index: nextIndex, low: nextIndex, onStack: true}
		nextIndex++
		stack = append(stack, o)
		for _, to := range graph[o] {
			if states[to] == nil {
				strongConnect(to)
				if states[to].low < states[o].low {
					states[o].low = states[to].low
				}
			} else if states[to].onStack && states[to].index < states[o].low {
				states[o].low = states[to].index
			}
		}
		if states[o].low != states[o].index {
			return
		}
		var component []gcNode
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			states[last].onStack = false
			component = append(component, last)
			if last == o {
				break
			}
		}
		if len(component) == 1 {
			self := false
			for _, to := range graph[o] {
				if to == o {
					self = true
					break
				}
			}
			if !self {
				return
			}
		}
		cyclic = append(cyclic, component...)
	}
	for o := range graph {
		if states[o] == nil {
			strongConnect(o)
		}
	}
	freed := map[gcNode]bool{}
	var include func(gcNode)
	include = func(o gcNode) {
		if freed[o] {
			return
		}
		freed[o] = true
		for _, to := range graph[o] {
			include(to)
		}
	}
	for _, o := range cyclic {
		include(o)
	}
	// A single handle has no release-order ambiguity. With several dead
	// objects, the engine's root-buffer order can differ from graph order.
	freedObjects := 0
	for n := range freed {
		if n.object != nil {
			freedObjects++
		}
	}
	if q.HandlesObserved && freedObjects > 1 {
		return 0, Unsupported("gc_collect_cycles with observable object handles")
	}
	if freedObjects > 0 && !c.ReleasesHandles() {
		return 0, Unsupported("gc_collect_cycles without object release hooks")
	}
	for n := range freed {
		if n.object != nil {
			c.ReleaseHandle(n.object)
		}
	}
	return len(freed), nil
}

// gcVisitRoots exposes request-owned values to a collector. A category that
// may hide a PHP value and cannot expose it makes the collection unsupported.
func (c *Ctx) gcVisitRoots(visit func(Value)) bool {
	if c == nil || c.pendingError != nil || len(c.requestEndClosers) != 0 || len(c.suspendedDrains) != 0 || c.resources.next != 0 {
		return false
	}
	if c.Globals != nil {
		visit(Arr(c.Globals))
	}
	for _, v := range c.constants {
		visit(v)
	}
	if c.assertions != nil {
		visit(c.assertions.Callback)
	}
	for _, v := range c.errorHandlers {
		visit(v)
	}
	for _, v := range c.exceptionHandlers {
		visit(v)
	}
	for _, v := range c.autoloaders {
		visit(v)
	}
	for _, call := range c.shutdown {
		visit(call.Callee)
		for _, v := range call.Args {
			visit(v)
		}
	}
	for _, frame := range c.stack {
		if frame.This != nil {
			visit(Obj(frame.This))
		}
		for _, v := range frame.Args {
			visit(v)
		}
	}
	if c.callSite != nil {
		if c.callSite.This != nil {
			visit(Obj(c.callSite.This))
		}
		for _, v := range c.callSite.Args {
			visit(v)
		}
	}
	for _, o := range c.handOver {
		visit(Obj(o))
	}
	for _, vals := range c.traceHeld {
		for _, v := range vals {
			visit(v)
		}
	}
	for _, v := range c.argStack {
		visit(v)
	}
	for _, o := range c.enteredFibers {
		visit(Obj(o))
	}
	if c.currentFiber != nil {
		visit(Obj(c.currentFiber))
	}
	for _, b := range c.buffers {
		if b == nil || b.filter == nil {
			continue
		}
		v, ok := b.filter.(Value)
		if !ok {
			return false
		}
		visit(v)
	}
	if len(c.streamWrappers) != 0 || len(c.streamFilters) != 0 {
		return false
	}
	return true
}
