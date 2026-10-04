package php

import "sort"

// Running destructors in compiled output.
//
// The engine fires `__destruct` at three points -- measured, not assumed:
//
//	unset($x) on the sole reference   immediately
//	a function local at return        immediately, before the caller resumes
//	script end                        the survivors, reverse order
//
// The first two need to know that a reference was the last one, which is the
// refcount problem and is not solved here. **This is the third alone**, and it
// is deliberately the safe half: an object registered here is destroyed exactly
// once, at the end, and never early. A destructor that the engine ran sooner
// still runs -- later than it should, in the wrong place in the output, but it
// runs, where before it did not run at all.
//
// Nothing registers unless the class has a destructor, so a program without one
// pays a nil check per object.

// RegisterDestructible records an object whose class declares `__destruct`, so
// that script-end teardown can reach it.
func (c *Ctx) RegisterDestructible(o *Object) {
	if o != nil {
		c.destructible = append(c.destructible, o)
	}
}

// DestructorRun is how the layer above runs one -- it needs a class table to
// find the body, which this package does not have.
type DestructorRun func(ctx *Ctx, o *Object) error

// SetDestructorRun installs the hook.
func (c *Ctx) SetDestructorRun(fn DestructorRun) { c.destructorRun = fn }

// MarkDestructed records that an object's destructor has run, so that teardown
// does not run it a second time. The engine guards the same way: an object
// destroyed during shutdown is not destroyed again when the store is torn down.
//
// A flag on the object, not a map on the context: the map kept every object
// that ever died reachable for the rest of the run, and `new Foo()` in a loop
// put five million entries in it.
func (c *Ctx) MarkDestructed(o *Object) {
	if o != nil {
		o.destructed = true
	}
}

// Destructed reports whether one has already run.
func (c *Ctx) Destructed(o *Object) bool { return o != nil && o.destructed }

// RunPendingDestructors destroys everything still alive at script end.
//
// **Reverse order**, which is what the engine does: three objects made in turn
// and none released are destroyed third, second, first. (That is the engine's
// global-symbol-table phase; SetHandleOrderTeardown switches to the
// objects-store walk when that phase is known to have nothing to do.) It runs *before* the
// registered shutdown functions, which is the opposite of the obvious order and
// was measured rather than assumed. An object a destructor makes is destroyed
// in the same teardown, **after** the walk that made it: `d2` making `$z`
// prints `d2`, `d1`, `dz`, then the shutdown function (probed).
//
// **A destructor that throws is uncaught**, and `uncaught` answers for it: the
// exception handler if one is installed, the fatal block otherwise. It reports
// whether the throw was fatal, and a fatal one **ends the walk** -- the objects
// not reached stay pending, and the engine destroys them only after the
// shutdown functions, in a second walk that the next fatal ends for good.
// Probed with three throwing destructors and a shutdown function: `d3`, its
// fatal, the shutdown function, `d2`, its fatal, and `d1` never runs. The throw
// was dropped here, and the engine was believed to print nothing for it.
//
// The list stays on the context while it is walked, so that a bailout
// `uncaught` answers with ForgetPendingDestructors reaches the objects not yet
// reached as well.
func (c *Ctx) RunPendingDestructors(uncaught func(error) bool) {
	if c.destructorRun == nil {
		return
	}
	defer c.dropDestructed()
	for {
		pending := c.destructible
		order := pending
		if c.handleOrder {
			order = byHandle(pending)
		}
		for n := 0; n < len(order); n++ {
			i := len(order) - 1 - n
			if c.handleOrder {
				i = n
			}
			o := order[i]
			if c.Destructed(o) {
				continue
			}
			if o.Lazy != nil && o.Lazy.Instance == nil {
				// **An uninitialized lazy object has no destructor call.** It
				// has no state, so there is nothing to destroy, and the engine
				// does not run one -- `dtor_not_called_if_not_init` is the test
				// that says so. A proxy that was initialized answers through
				// its instance.
				continue
			}
			if o.Lazy != nil && o.Lazy.SkipDestructor {
				continue
			}
			c.MarkDestructed(o)
			// **The engine calls it, not a line of the script**: its trace
			// frame is `#0 [internal function]: C->__destruct()`, where the
			// last line the body ran would otherwise stand as the call site.
			c.SetLocation(NoActiveFile, 0)
			c.shutdownDtorDepth = len(c.stack) + 1
			err := c.destructorRun(c, o)
			c.shutdownDtorDepth = 0
			// **A local that died throwing is an uncaught exception of its
			// own**, reported before the destructor's: at shutdown the engine
			// hands the handler `from inner`, then `from outer`, neither
			// chained to the other, and without a handler the inner one's
			// fatal is all there is. It was parked where the body's frame
			// drained, and the handler it then leaked into returned it as the
			// handler's own throw.
			if parked := c.TakePendingError(); parked != nil && uncaught(parked) {
				return
			}
			if err != nil && uncaught(err) {
				return
			}
		}
		if len(c.destructible) == len(pending) {
			return
		}
		c.dropDestructed()
	}
}

// SetHandleOrderTeardown switches script-end teardown to the engine's
// **objects-store walk**: every object still alive, in ascending handle order,
// an object a destructor makes along the way visited as the walk reaches it.
//
// The engine ends a request in two phases (probed). First it walks the global
// symbol table backwards and destroys each global that holds an object nothing
// else holds -- that is the reverse order RunPendingDestructors otherwise
// models, and it needs refcounts to know "nothing else". Then it walks the
// objects store from handle 1 up, with handle reuse switched off, so the
// objects that phase creates get new handles above every other and are
// reached in turn:
//
//	class A { static $s, $t; }  A::$s = new D('s'); A::$t = new D('t');
//	    -> ds, dt                          (not dt, ds)
//	A::$p = new D; $t = new stdClass; A::$q = new D; unset($t); A::$r = new D;
//	    -> p#1, r#2, q#3                   (handle order, not creation order)
//
// The layer above sets it **only when the first phase has nothing to do**: no
// global holds an object, so no refcount is asked and the second walk is the
// whole teardown (bug74053). The caller also stops handing freed handles out
// again, as the engine does from here on.
func (c *Ctx) SetHandleOrderTeardown(on bool) { c.handleOrder = on }

// byHandle is the objects in ascending handle order. Stable, so objects with
// the same number -- one whose number was handed back while it stayed pending
// and the object that reused it -- keep their creation order.
func byHandle(objs []*Object) []*Object {
	out := append([]*Object(nil), objs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// dropDestructed takes the objects already destroyed off the list.
func (c *Ctx) dropDestructed() {
	live := c.destructible[:0]
	for _, o := range c.destructible {
		if !c.Destructed(o) {
			live = append(live, o)
		}
	}
	clear(c.destructible[len(live):])
	c.destructible = live
}

// ForgetPendingDestructors marks every object still waiting as destroyed,
// without running a destructor.
//
// **A fatal error does this**: after one -- in the body, in a destructor at
// shutdown, in a shutdown function -- no object that existed then is destroyed,
// while one a later shutdown function makes still is (probed: `$GLOBALS['c'] =
// new C` in a shutdown function after a fatal prints `d3`, and the survivors
// print nothing).
func (c *Ctx) ForgetPendingDestructors() {
	for _, o := range c.destructible {
		c.MarkDestructed(o)
	}
	c.destructible = nil
}

// RunConfinedDestructor destroys an object whose frame is ending.
//
// Used for an object the compiler proved cannot outlive the function that made
// it. PHP destroys a local when its frame is destroyed, so this is the engine's
// own timing rather than an approximation -- and because the emitter defers it,
// several in one frame run in reverse order, which is also the engine's.
//
// Idempotent: an object already destroyed is left alone, and one destroyed here
// is skipped by script-end teardown.
func (c *Ctx) RunConfinedDestructor(v Value) {
	o := v.Deref().Object()
	if o == nil || c.destructorRun == nil || c.Destructed(o) {
		return
	}
	if o.NoDestructor && o.Lazy == nil {
		c.MarkDestructed(o)
		return
	}
	if o.Lazy != nil && (o.Lazy.Instance == nil || o.Lazy.SkipDestructor) {
		return
	}
	// **An initialized proxy has no destructor of its own.** The engine runs
	// the instance's when the instance dies; a proxy that was its only
	// holder takes it with it, and runs that destructor here.
	if InitializedProxy(o) {
		c.MarkDestructed(o)
		if inst := OwnedProxyInstance(o); inst != nil {
			c.RunConfinedDestructor(Obj(inst))
		}
		return
	}
	c.MarkDestructed(o)
	// **A destructor that throws throws from where the object died**: the
	// caller's `catch` around a call whose frame destroyed it, the code
	// after an `unset`, a loop's end. This runs where no error can be
	// returned -- a deferred frame drain among them -- so the exception is
	// parked for the next opcode boundary. It was dropped.
	c.ParkError(c.destructorRun(c, o))
}

// LeaveFrame takes the innermost call off the stack while its frame's objects
// are destroyed, and puts it back when `restore` is called.
//
// **A local dies after its frame has gone**, as far as a trace can tell: the
// engine reports a destructor run as `f()` returns as called from `f()`'s own
// call site -- `#0 file(4): I->__destruct()`, `#1 {main}` -- with no `f()`
// frame in between, whether `f()` returned or threw (probed). At shutdown the
// call site is the engine's, so the destructor a destructor's local runs is
// `#0 [internal function]: I->__destruct()`.
//
// The frame is copied rather than left in place, since a call made meanwhile
// is pushed into the same slot.
func (c *Ctx) LeaveFrame() (restore func()) {
	n := len(c.stack)
	if n == 0 {
		return func() {}
	}
	frame := c.stack[n-1]
	c.stack = c.stack[:n-1]
	file, line := c.file, c.line
	if frame.File != "" {
		c.file, c.line = frame.File, frame.Line
	}
	return func() {
		c.stack = append(c.stack[:n-1], frame)
		c.file, c.line = file, line
	}
}

// DrainingShutdownDestructor reports that the frame whose locals are being
// destroyed is a destructor the request-end walk called. There, and only
// there, a local that dies throwing is not chained onto the exception the
// destructor is leaving with: the engine reports the two as separate uncaught
// exceptions, the local's first (probed). A call such a destructor makes, and
// every frame mid-script, chains as usual. Asked before the frame is taken
// off the stack.
func (c *Ctx) DrainingShutdownDestructor() bool {
	return c.shutdownDtorDepth > 0 && len(c.stack) == c.shutdownDtorDepth
}
