package php

import "errors"

// Binding a PHP callable to the frame a Fiber drives.
//
// `new Fiber($callback)` does not call anything, and `$f->start(...)` does not
// call it either in the sense the rest of the runtime means by "call": the body
// has to be *entered* so that a `Fiber::suspend()` anywhere inside it comes
// back here as an `Outcome` rather than unwinding through a Go return. So what
// a Fiber needs from a callable is its **frame**, not its result.
//
// Supplied from above, because only the emitter knows which bodies were
// compiled as state machines and how to build one.

// ResumableMaker builds the frame behind a callable without running it.
//
// It reports handled=false for a callable that exists but was not compiled as a
// state machine -- an ordinary function passed to `new Fiber` that never
// suspends. That is not an error: such a body runs to completion on the first
// `start()`, and the caller falls back to an ordinary call.
type ResumableMaker func(ctx *Ctx, callee Value, args []Value) (Resumable, bool, error)

// SetResumableMaker installs the hook.
func (c *Ctx) SetResumableMaker(fn ResumableMaker) { c.resumableMaker = fn }

// ResumableOf applies it.
func (c *Ctx) ResumableOf(callee Value, args []Value) (Resumable, bool, error) {
	if c.resumableMaker == nil {
		return nil, false, nil
	}
	return c.resumableMaker(c, callee, args)
}

// CurrentFiber is what `Fiber::getCurrent()` answers: the **object** the
// program holds, since that is what it gets handed back. It is the fiber whose
// body is running right now, which is not the same as the most recently started
// one -- fibers nest, and resuming one from inside another makes the inner one
// current until it gives control back.
func (c *Ctx) CurrentFiber() *Object { return c.currentFiber }

// EnterFiber makes one current and answers the restore. Paired with a defer at
// every place that hands control into a body, so the nesting is a stack.
func (c *Ctx) EnterFiber(o *Object) func() {
	saved := c.currentFiber
	c.currentFiber = o
	c.enteredFibers = append(c.enteredFibers, o)
	depth := len(c.enteredFibers)
	return func() {
		c.currentFiber = saved
		if len(c.enteredFibers) >= depth {
			c.enteredFibers = c.enteredFibers[:depth-1]
		}
	}
}

// FiberSwitchBlocked reports whether control may pass between fibers at all.
// Some execution contexts the engine enters -- a tick handler, a destructor run
// at shutdown -- refuse the switch, and the diagnostic says so rather than
// deadlocking.
func (c *Ctx) FiberSwitchBlocked() bool { return c.fiberSwitchBlocked > 0 }

// BlockFiberSwitch enters such a context and answers the exit.
func (c *Ctx) BlockFiberSwitch() func() {
	c.fiberSwitchBlocked++
	return func() { c.fiberSwitchBlocked-- }
}

// FiberHandle is the state behind a `Fiber` object the program holds.
//
// The driver does not exist until `start()`: a fiber's frame is built from the
// callable **and the arguments start() was given**, and those are not known at
// construction. `new Fiber($fn)` therefore records only the callable, which is
// also why constructing one runs nothing.
type FiberHandle struct {
	Callable Value
	Driver   *Fiber
	// Silence is the fiber's own error-reporting state while it is not the
	// one running (SilenceState).
	Silence SilenceState
}

// FiberErrorMessage maps a driver sentinel to the message PHP prints for it.
// The drivers report conditions rather than exceptions -- `runtime/php` has no
// class table to construct one with -- so the layer that does the mapping needs
// the wording in one place rather than spelled out at each call site.
func FiberErrorMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, ErrFiberAlreadyStarted):
		return "Cannot start a fiber that has already been started", true
	case errors.Is(err, ErrFiberNotSuspended):
		return "Cannot resume a fiber that is not suspended", true
	case errors.Is(err, ErrFiberNotStarted):
		return "Cannot get fiber return value: The fiber has not been started", true
	case errors.Is(err, ErrFiberNotReturned):
		return "Cannot get fiber return value: The fiber has not returned", true
	case errors.Is(err, ErrFiberThrew):
		return "Cannot get fiber return value: The fiber threw an exception", true
	}
	return "", false
}

// ReturnedFiber is a fiber that is already over: the callable had no
// `Fiber::suspend()` in it, so `start()` ran it to completion and there was
// never anything to park. The object still answers `isTerminated()` and
// `getReturn()`, which is why it needs a driver at all.
func ReturnedFiber(v Value) *Fiber {
	return &Fiber{status: FiberTerminated, returned: true, ret: v}
}

// ThrewFiber is the same for a callable that ended by throwing. `getReturn()`
// on one is a refusal rather than a value, and it is a *different* refusal from
// the one a still-running fiber earns.
func ThrewFiber() *Fiber {
	return &Fiber{status: FiberTerminated, threw: true, ret: Null()}
}
