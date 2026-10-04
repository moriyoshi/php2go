package interp

import (
	"github.com/moriyoshi/php2go/runtime/php"
)

// Collecting what the program can no longer reach.
//
// php2go has no refcounts: a `php.Value` holding an object is a Go pointer, and
// Go copies one wherever it likes. So there is no moment at which the runtime
// can be told "that was the last reference". What it *can* do is answer the
// same question the other way round -- walk the roots and see what is still
// reachable -- and that is enough for the two things a program can observe: a
// destructor running, and a weak reference going empty.
//
// The walk is only worth doing when there is something to find, which is why
// every entry point checks first. A program with no destructors and no weak
// references never pays for any of this, and that is nearly every program.

// collect runs the destructors of every object the program can no longer
// reach, and empties the weak references that pointed at them. It answers how
// many objects it destroyed, which is what gc_collect_cycles reports.
func (in *Interp) collect(ctx *php.Ctx, unit *Unit) int {
	count, _ := in.collectKind(ctx, unit, true, true)
	return count
}

// maxCollectDepth bounds the cascade of collections a destructor can set off by
// releasing something whose destructor releases something else. Past it the
// rest waits for the next release point, which is what php2go did everywhere
// before.
const maxCollectDepth = 64

// collectKind collects what the program can no longer reach. When cycles is
// false only the garbage PHP's refcount would already have freed is destroyed
// -- the objects nothing else dead is holding -- and anything caught in a cycle
// is left for gc_collect_cycles, which is where PHP frees it and where the
// count it reports comes from.
func (in *Interp) collectKind(ctx *php.Ctx, unit *Unit, cycles, counting bool) (int, error) {
	if unit.collecting || (!counting && !unit.worthCollecting()) {
		return 0, nil
	}
	// A destructor can drop the last reference to something else. That is a
	// second collection's business, not a nested one's: re-entering here while
	// the walk's answer is already stale would run destructors against it.
	unit.collecting = true
	defer func() { unit.collecting = false }()
	unit.collectDepth++
	defer func() { unit.collectDepth-- }()
	marked := unit.reachableMarker(ctx)
	reachable := marked.seen
	if !unit.keeping.IsUndef() {
		// A value the caller is still holding but that no slot names yet --
		// the result of a call that has just returned, on its way to a
		// temporary.
		marked.value(unit.keeping)
	}
	// What the walk cost: everything it marked, plus the object list it has to
	// sweep afterwards. Cyclic garbage is not marked and is not freed by a
	// release point either, so it stays on the list -- and without counting it
	// a program building cycles in a loop would sweep a growing list at a
	// fixed cadence, which is quadratic.
	unit.lastWalk = len(marked.seen) + len(marked.arrays) + len(unit.objects)
	// A suspended fiber nobody can reach will never be resumed, so it is
	// unwound here -- its finally blocks run at the point the program lost it,
	// which is where PHP runs them too.
	var thrown error
	for _, held := range unit.fibers {
		if held != nil && held.object != nil && !reachable[held.object] {
			if err := in.forceCloseFor(ctx, unit, held); err != nil {
				thrown = chainOnto(err, thrown)
			}
		}
	}
	// A suspended generator nobody can reach is in the same position, and for
	// the same reason: its finally blocks belong to the point the program lost
	// it -- and so does whatever they throw, which goes to the try that is open
	// there rather than straight to the output.
	for _, object := range unit.objects {
		if reachable[object] {
			continue
		}
		if held, ok := asGenerator(php.Obj(object)); ok {
			if err := in.closeDelegatingGenerator(ctx, unit, held, reachable); err != nil {
				thrown = chainOnto(err, thrown)
			}
		}
	}

	// The count answers for the graph as it stands *before* any destructor
	// runs, since a destructor can make more values unreachable -- and the
	// engine's collector likewise decides what it is freeing before it starts.
	cycleTotal := 0
	if counting {
		cycleTotal = unit.cycleCount(marked)
	}

	// Which of the unreachable objects PHP would have freed by refcount alone:
	// the ones no other dead object holds. Everything else is in a cycle.
	held := unit.deadReferences(reachable)

	collected := 0
	// Freeing an object releases what it held, which may be the last thing
	// holding something else -- PHP's refcount would cascade down a chain in
	// one go, so the sweep is a worklist rather than a single pass over the
	// list. Without it, `new B()` whose constructor stashed an A in a property
	// would destruct B now and leave A for the next release point, or for
	// shutdown if there is not one.
	queue := make([]*php.Object, 0, 8)
	for _, object := range unit.objects {
		if object != nil && !reachable[object] && (cycles || held[object] == 0) {
			queue = append(queue, object)
		}
	}
	gone := map[*php.Object]bool{}
	ranDestructor := false
	for len(queue) > 0 {
		object := queue[0]
		queue = queue[1:]
		if gone[object] {
			continue
		}
		gone[object] = true
		collected++
		if unit.hasDestructorPending(object) {
			ranDestructor = true
			if err := in.destructAt(ctx, unit, object); err != nil {
				// A destructor that throws does so at the point in the program
				// the collection happened, so the exception goes to whatever
				// try is open there. The remaining destructors still run, and
				// what escapes is the *last* of them: each new exception takes
				// the one already in flight as its cause, which is what the
				// engine prints as "Next Exception".
				thrown = chainOnto(err, thrown)
			}
		}
		// A typed property's declaration is a constraint on whatever reference
		// the property shares, and the property goes away with the object:
		// `$r =& $b->prop; unset($b); $r = "foo";` is allowed, because by then
		// nothing declares the slot. Dropping the sources here is
		// `zend_ref_del_type_source` running as the slots are freed.
		dropTypeSources(object)
		// The engine notifies weak references when the target is *freed*, and
		// freeing it is what then drops what it was holding -- so a later
		// destructor in the same sweep already sees the reference cleared.
		unit.pruneWeakTarget(ctx, object)
		// The engine's object store hands the handle of a freed object back to
		// the next `new`, and var_dump prints it, so a program can see the
		// difference.
		unit.freeHandles = append(unit.freeHandles, object.ID)
		if cycles {
			continue
		}
		// What this one was holding may now have nothing left holding it.
		unit.edgesFrom(object, func(target *php.Object) {
			if reachable[target] || gone[target] || held[target] == 0 {
				return
			}
			held[target]--
			if held[target] == 0 {
				queue = append(queue, target)
			}
		})
	}
	if len(gone) > 0 {
		// Dropping the objects from the list is what keeps them from being
		// freed twice, and what keeps the list from being every object the
		// request ever made.
		kept := unit.objects[:0]
		for _, object := range unit.objects {
			if object != nil && !gone[object] {
				kept = append(kept, object)
			}
		}
		for i := len(kept); i < len(unit.objects); i++ {
			unit.objects[i] = nil
		}
		unit.objects = kept
	}
	if ranDestructor && unit.collectDepth < maxCollectDepth {
		// A destructor can drop the last name on something else -- writing null
		// over a property, unsetting a global. The engine frees that at once,
		// by refcount, so the second object's destructor runs before the opline
		// that set off the first one finishes. php2go's answer came from a walk
		// taken *before* the destructor ran, so it takes another one here
		// rather than leaving the second object for the next release point.
		//
		// It happens after the list has been pruned, so nothing is swept twice,
		// and it terminates: a pass only repeats when it ran a destructor, and
		// a destructor runs once per object.
		unit.collecting = false
		if _, err := in.collectKind(ctx, unit, false, false); err != nil {
			thrown = chainOnto(err, thrown)
		}
		unit.collecting = true
	}
	if counting {
		// Everything unreachable has now been freed, in the engine's terms,
		// so it is out of the running for the next count -- and dropping it
		// keeps the list from being every object the request ever made.
		unit.forget(marked)
		// gc_collect_cycles() answers with what the *cycle* collector freed,
		// which is not the same as everything that turned out to be
		// unreachable. See cycles.go.
		return cycleTotal, thrown
	}
	return collected, thrown
}

// deadReferences counts, for each unreachable object, how many other
// unreachable containers hold it. An object nothing dead holds is what PHP's
// refcount would have freed at the moment the last name went away; the rest are
// a cycle, and only gc_collect_cycles frees those.
func (u *Unit) deadReferences(reachable map[*php.Object]bool) map[*php.Object]int {
	held := map[*php.Object]int{}
	for _, object := range u.objects {
		if object == nil || reachable[object] || u.destructed[object] {
			continue
		}
		u.edgesFrom(object, func(target *php.Object) {
			if !reachable[target] {
				held[target]++
			}
		})
	}
	return held
}

// edgesFrom reports every object this one holds directly. An array in a
// property is part of the object, so the walk goes through arrays; it stops at
// the far end of each object edge, because that object's own edges belong to
// it and not to this one.
func (u *Unit) edgesFrom(object *php.Object, visit func(*php.Object)) {
	m := &marker{
		seen:    map[*php.Object]bool{},
		arrays:  map[*php.Array]bool{},
		count:   visit,
		shallow: true,
	}
	m.contents(object)
}

// smallHeap is how many destructible objects a program may have before
// collecting at every release point stops being affordable.
//
// A collection walks everything reachable, so doing one each time a name is
// dropped is quadratic in the number of live objects. Below this many the
// quadratic term does not matter and the timing is exact, which is what a test
// watching for a destructor needs. Above it the walks are batched, and a
// destructor may run later than PHP's refcount would have run it -- the same
// trade the engine makes with its own root-buffer threshold.
const smallHeap = 512

// releaseBudget is how many release points may pass without a walk once the
// heap is past smallHeap.
const releaseBudget = 256

// releaseCheck runs a collection after a name has stopped holding something.
// PHP would have noticed the refcount reach zero at exactly this point; php2go
// asks the question by walking, and only when there is an answer worth having.
// releaseCheckKeeping is releaseCheck for a moment when something is live but
// unnamed: the value a call has just produced, before the opcode that stores
// it has run.
func (in *Interp) releaseCheckKeeping(ctx *php.Ctx, unit *Unit, keep php.Value) error {
	previous := unit.keeping
	unit.keeping = keep
	defer func() { unit.keeping = previous }()
	return in.collectBudgeted(ctx, unit)
}

func (in *Interp) releaseCheck(ctx *php.Ctx, unit *Unit, released php.Value) error {
	kind := released.Deref().Kind()
	if kind != php.KindObject && kind != php.KindArray {
		return nil
	}
	return in.collectBudgeted(ctx, unit)
}

// collectBudgeted walks unless the heap is large enough that walking at every
// release point would cost more than the timing is worth. A release point is
// not the cycle collector, so anything caught in a cycle is left alone.
func (in *Interp) collectBudgeted(ctx *php.Ctx, unit *Unit) error {
	if len(unit.objects) > smallHeap {
		unit.sinceCollect++
		// The budget is at least what the last walk cost, so the work spent
		// walking is at worst proportional to the work the program did between
		// two walks. A program holding a hundred thousand objects would
		// otherwise spend all its time re-proving they are still reachable.
		budget := releaseBudget
		if unit.lastWalk > budget {
			budget = unit.lastWalk
		}
		if unit.sinceCollect < budget {
			return nil
		}
		unit.sinceCollect = 0
	}
	_, err := in.collectKind(ctx, unit, false, false)
	return err
}

// hasDestructorPending reports whether an object still has a destructor to run.
func (u *Unit) hasDestructorPending(object *php.Object) bool {
	if u.destructed[object] || object.Lazy != nil {
		return false
	}
	class, known := u.findClass(object.ClassName)
	return known && u.hasDestructor(class)
}

// worthCollecting reports whether anything could observe a collection. Three
// things can: a destructor still to run, a weak container that could drop an
// entry, and -- since PHP prints object handles and hands a freed one back out
// to the next `new` -- any object at all.
func (u *Unit) worthCollecting() bool {
	// Counted, not scanned: this runs at every release point, so walking the
	// object list here would be the quadratic term the budget exists to avoid.
	return len(u.objects) > 0 || len(u.weakContainers) > 0
}

// reachableMarker marks everything the program can still get to, answering the
// marking itself: gc_collect_cycles() needs the arrays as well as the objects.
func (u *Unit) reachableMarker(ctx *php.Ctx) *marker {
	m := u.storedMarker(ctx)
	m.array(ctx.Globals)
	return m
}

// storedMarker marks everything reachable from somewhere that outlives the
// global symbol table: a static property, a function static, an enum case, a
// registered shutdown call. Tearing the globals down does not free what one of
// these still holds, which is why the end of a request destroys such an object
// after the symbol table rather than with it.
func (u *Unit) storedMarker(ctx *php.Ctx) *marker {
	m := &marker{seen: map[*php.Object]bool{}, arrays: map[*php.Array]bool{}}
	// Every frame still executing, innermost or not: its locals, its arguments
	// and its receiver are all live.
	for _, f := range u.frames {
		m.frame(f)
	}
	for _, value := range ctx.Constants() {
		m.value(value)
	}
	for _, object := range u.building {
		m.object(object)
	}
	for _, ref := range u.staticProps {
		if ref != nil {
			m.value(ref.Get())
		}
	}
	for _, value := range u.statics {
		m.value(value)
	}
	for _, value := range u.enumCases {
		m.value(value)
	}
	for _, value := range u.defaults {
		m.value(value)
	}
	for _, handler := range ctx.ExceptionHandlers() {
		m.value(handler)
	}
	// A callback the program registered with the engine is held by the engine,
	// not by the program: an autoloader closure survives the statement that
	// registered it, and its handle is not handed out again.
	for _, value := range ctx.Autoloaders() {
		m.value(value)
	}
	for _, value := range ctx.ErrorHandlers() {
		m.value(value)
	}
	for _, call := range ctx.ShutdownCalls() {
		m.value(call.Callee)
		for _, arg := range call.Args {
			m.value(arg)
		}
	}
	// unit.fibers is bookkeeping, not a reference: a fiber nobody holds is
	// unreachable however long the list remembers it. One that *is* held is
	// marked through its object like anything else, which is what carries its
	// suspended frames.
	return m
}

// marker walks values, remembering what it has already been through so a cycle
// terminates.
type marker struct {
	seen   map[*php.Object]bool
	arrays map[*php.Array]bool
	// frames guards the walk against a frame that reaches itself. A generator
	// delegating to itself with `yield from` is exactly that shape, and the
	// walk has no other way to notice: a frame is not an object.
	frames map[*frame]bool
	// count, when set, is called for every object an edge points at instead of
	// the walk being about reachability. It is how the same traversal serves
	// both marking and counting who holds whom.
	count func(*php.Object)
	// shallow stops the walk at each object it reaches, so what it reports is
	// the edges *from* the starting point rather than everything below it.
	// Counting who holds whom needs that: an edge belongs to the object it
	// leaves, and a transitive walk would credit it to the wrong one.
	shallow bool
}

func (m *marker) value(v php.Value) {
	v = v.Deref()
	switch v.Kind() {
	case php.KindObject:
		m.object(v.Object())
	case php.KindArray:
		m.array(v.Array())
	}
}

func (m *marker) array(a *php.Array) {
	if a == nil || m.arrays[a] {
		return
	}
	m.arrays[a] = true
	a.Each(func(_ php.Key, item php.Value) bool {
		m.value(item)
		return true
	})
}

func (m *marker) object(o *php.Object) {
	if o == nil {
		return
	}
	if m.count != nil {
		m.count(o)
	}
	if m.shallow {
		return
	}
	if m.seen[o] {
		return
	}
	m.seen[o] = true
	m.contents(o)
}

// contents walks what an object holds, without visiting the object itself.
func (m *marker) contents(o *php.Object) {
	m.array(o.Props)
	if o.Lazy != nil {
		m.value(o.Lazy.Initializer)
		m.object(o.Lazy.Instance)
	}
	switch held := o.Internal.(type) {
	case *closure:
		for _, value := range held.bound {
			m.value(value)
		}
		m.object(held.this)
	case *generator:
		m.frame(held.frame)
		m.value(held.returned)
		m.value(held.sent)
	case *fiber:
		m.value(held.callable)
		m.value(held.returned)
		for _, f := range held.frames {
			m.frame(f)
		}
	case *arrayStore:
		m.array(held.Items())
	case *throwableState:
		// An exception holds the arguments of every frame it captured, and
		// they are as good a root as any name: `$e->getTrace()[0]['args'][0]`
		// hands one back, and a destructor must not have run in the meantime.
		for _, frame := range held.trace {
			for _, arg := range frame.Args {
				m.value(arg)
			}
			// Not frame.This: an exception's trace reports the arguments and
			// not the receiver, so `$this` of the frame that threw is not kept
			// alive by the exception. A half-built object whose constructor
			// threw is destroyed at the call site, not at the end of the
			// request.
		}
	case *reflector:
		m.object(held.bound)
		m.object(held.subject)
	case *weakMap, *weakRef:
		// Deliberately not walked: a weak container is the one thing that
		// holds an object without keeping it alive.
	}
}

func (m *marker) frame(f *frame) {
	if f == nil {
		return
	}
	if m.frames == nil {
		m.frames = map[*frame]bool{}
	}
	if m.frames[f] {
		return
	}
	m.frames[f] = true
	// Only the compiled variables are roots. The temporaries after them are
	// consumed by the opcode that reads them, and php2go does not clear them
	// afterwards -- so every object a statement ever built would look reachable
	// for the rest of the frame's life. Anything a temporary legitimately holds
	// right now is also held by the call being assembled, the iterator being
	// walked, or the exception in flight, all of which are walked below.
	names := len(f.op.GetVarNames())
	for i, slot := range f.slots {
		if i >= names {
			break
		}
		m.value(slot)
	}
	for _, arg := range f.args {
		m.value(arg)
	}
	// An array literal still being assembled lives only in a temporary.
	for _, literal := range f.literals {
		m.value(literal.value)
	}
	m.object(f.this)
	for _, value := range f.bound {
		m.value(value)
	}
	for _, named := range f.extraNamed {
		m.value(named.value)
	}
	m.value(f.yieldKey)
	m.value(f.yieldValue)
	for _, unwinding := range f.unwinding {
		m.object(unwinding.object)
	}
	// Calls being assembled hold their arguments and their receiver, and an
	// object may be reachable through nothing else while it is in flight.
	pending := f.calls
	if f.callsRef != nil {
		pending = *f.callsRef
	}
	for _, call := range pending {
		if call == nil {
			continue
		}
		m.object(call.this)
		m.object(call.ctorObject)
		for _, arg := range call.args {
			m.value(arg)
		}
		for _, named := range call.named {
			m.value(named.value)
		}
	}
	for _, it := range f.iterators {
		m.iterator(it)
	}
	// A `yield from` in progress holds its source, and holds it *only* here:
	// the delegate is not a variable, so nothing above reaches it. A generator
	// draining another one is the case that matters -- the inner generator is
	// reachable through this and nothing else.
	m.iterator(f.delegate)
}

// iterator marks what a walk in progress is holding.
func (m *marker) iterator(it *iterator) {
	if it == nil {
		return
	}
	m.array(it.array)
	m.array(it.backing)
	m.object(it.backingOwner)
	m.object(it.origin)
	m.object(it.obj)
	m.value(it.returned)
	if it.gen != nil {
		m.object(it.gen.object)
		m.frame(it.gen.frame)
	}
}

// pruneWeak empties the entries of every weak container whose target the
// program can no longer reach. This is what makes a WeakReference answer null
// and a WeakMap shrink.
func (u *Unit) pruneWeak(reachable map[*php.Object]bool) {
	for _, holder := range u.weakContainers {
		switch held := holder.Internal.(type) {
		case *weakMap:
			held.Sweep(reachable)
		case *weakRef:
			held.Clear(reachable)
		}
	}
}

// dropTypeSources releases the declarations an object's properties imposed on
// references they share. See the call site: a reference outlives the object it
// was taken from, and stops being typed when the object goes.
func dropTypeSources(object *php.Object) {
	if object == nil {
		return
	}
	for _, key := range object.Props.Keys() {
		held := object.Props.RefHeld(key)
		if held == nil {
			continue
		}
		name := key.String()
		owner := object.Declared[name].Class
		if owner == "" {
			owner = object.ClassName
		}
		held.DropSourceOwned(object, owner, name)
	}
}

// pruneWeakTarget drops the entries naming one object, at the point that
// object is freed. A destructor may have stored `$this` somewhere reachable,
// which resurrects the object: it is then not freed at all, so the entry
// stands. Pruning is destructive, so answering that question before the
// destructors ran would clear an entry nothing can put back.
func (u *Unit) pruneWeakTarget(ctx *php.Ctx, object *php.Object) {
	if len(u.weakContainers) == 0 || !u.weaklyHeld(object) {
		return
	}
	if u.reachableMarker(ctx).seen[object] {
		return
	}
	for _, holder := range u.weakContainers {
		switch held := holder.Internal.(type) {
		case *weakMap:
			held.Drop(object)
		case *weakRef:
			held.DropTarget(object)
		}
	}
}

// weaklyHeld answers whether any weak container names this object, so the
// walk pruneWeakTarget would otherwise need is only paid when it can matter.
func (u *Unit) weaklyHeld(object *php.Object) bool {
	for _, holder := range u.weakContainers {
		switch held := holder.Internal.(type) {
		case *weakMap:
			for _, entry := range held.Entries() {
				if entry.Key() == object {
					return true
				}
			}
		case *weakRef:
			if held.Target() == object {
				return true
			}
		}
	}
	return false
}

// observeWeak answers the question a weak container is about, at the moment
// something looks at it. php2go has no refcounts, so an entry whose key has
// become unreachable is still there until a walk notices -- and reading the
// container is exactly when the answer has to be right. Nothing is destroyed
// here: this is only the pruning, which is what the reader can see.
func (u *Unit) observeWeak(ctx *php.Ctx) {
	if len(u.weakContainers) == 0 || u.collecting {
		return
	}
	u.pruneWeak(u.reachableMarker(ctx).seen)
}

// noteWeak registers a weak container so a collection can prune it. There are
// few of them and they are never removed, which keeps the bookkeeping to the
// one place they are made.
func (u *Unit) noteWeak(object *php.Object) {
	u.weakContainers = append(u.weakContainers, object)
}

// chainOnto records an exception already in flight as the cause of a newer one
// and answers with the newer. It is what a second throwing destructor does to
// the first: both are reported, the newer one first and the older beneath it.
//
// A link is left unmade when the newer one already has a cause, or when making
// it would close a loop -- the engine will not print a chain that cannot end.
func chainOnto(newer, older error) error {
	if older == nil {
		return newer
	}
	fresh, freshOK := asThrown(newer)
	stale, staleOK := asThrown(older)
	if !freshOK || !staleOK || fresh.object == nil || stale.object == nil ||
		fresh.object == stale.object {
		return newer
	}
	if held, ok := fresh.object.Get("previous"); ok &&
		held.Deref().Kind() == php.KindObject {
		return newer
	}
	if chainsTo(stale.object, fresh.object) {
		return newer
	}
	fresh.object.Set("previous", php.Obj(stale.object))
	return newer
}

// closeDelegatingGenerator unwinds a lost generator innermost-first. A
// `yield from` chain is one list in the engine, whose *root* is the generator
// actually running; giving up the outer one releases the root before it
// unwinds itself, so the inner `finally` runs first. Walking `unit.objects` in
// creation order would get it backwards, since the outer generator is made
// before the one it delegates to.
func (in *Interp) closeDelegatingGenerator(ctx *php.Ctx, unit *Unit, g *generator, reachable map[*php.Object]bool) error {
	// Collect the chain top-down, then unwind it bottom-up. The bound is the
	// same one `innermost` uses: nothing here should hang on a cycle the
	// compiler refuses to build.
	chain := []*generator{g}
	for hops := 0; hops < maxDelegateHops; hops++ {
		outer := chain[len(chain)-1]
		if outer.frame == nil || outer.frame.delegate == nil {
			break
		}
		inner := outer.frame.delegate.gen
		if inner == nil || inner.object == nil || reachable[inner.object] {
			break
		}
		chain = append(chain, inner)
	}
	var thrown error
	for i := len(chain) - 1; i >= 0; i-- {
		if err := in.closeGeneratorFor(ctx, unit, chain[i]); err != nil {
			thrown = chainOnto(err, thrown)
		}
	}
	return thrown
}
