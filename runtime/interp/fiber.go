package interp

import (
	"errors"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

// errFiberExit unwinds a fiber that will never be resumed. It is not an
// exception: `finally` blocks run, no `catch` clause is offered it, and it
// stops at the fiber's own boundary rather than escaping into the resumer.
var errFiberExit = errors.New("fiber exit")

// PHP 8.1's Fiber. A generator can be modelled by saving a program counter,
// because it only ever suspends at a `yield` written in its own body. A fiber
// cannot: `Fiber::suspend()` may be called from anywhere in the call stack it
// started, ten frames deep in code that knows nothing about fibers, and the
// whole stack has to stop where it is and resume there later.
//
// Go already has exactly that, so a fiber is a goroutine. What makes it a
// *fiber* rather than a thread is the handoff: control passes on an unbuffered
// channel, so the resumer blocks while the fiber runs and the fiber blocks
// while anything else does. Precisely one of them is ever running, which is the
// whole contract.
//
// The interpreter's per-request state -- the frame stack, the call stack, the
// executing scope -- belongs to whichever side is running. Each switch swaps it
// out and back, so a backtrace taken while a fiber is suspended does not see
// the fiber's frames, and one taken inside a fiber does not see its resumer's.

// fiberClasses are the engine classes this file implements.
var fiberClasses = map[string]bool{
	"fiber": true,
}

// resumption is what is handed *into* a fiber: a value for `Fiber::suspend()`
// to return, or an exception for it to throw.
type resumption struct {
	value php.Value
	throw error
}

// suspension is what comes *out* of a fiber: the value passed to
// `Fiber::suspend()`, or the fact that the fiber has finished.
type suspension struct {
	value    php.Value
	finished bool
	// err is how the fiber ended when it did not return normally: an escaped
	// exception, an exit(), or a gap in this runtime.
	err error
}

// fiber is the state behind one Fiber object.
type fiber struct {
	// object is the Fiber the program holds, which Fiber::getCurrent answers.
	object   *php.Object
	callable php.Value
	toFiber  chan resumption
	fromFip  chan suspension

	started    bool
	running    bool
	suspended  bool
	terminated bool
	returned   php.Value
	// failed records how a terminated fiber ended, so getReturn can refuse.
	failed bool
	// reporting is the fiber's own error_reporting level. It is inherited when
	// the fiber first starts -- from the level with any `@` disregarded -- and
	// from then on the two sides keep their own: error_reporting() called
	// inside a fiber does not reach its resumer, and an `@` around the resume
	// does not reach the fiber.
	reporting    int64
	reportingSet bool
	// closing is set while the fiber is being unwound because nothing will
	// resume it. Suspending again during that unwind has nowhere to go back to.
	closing bool

	// The interpreter state the fiber owns while it is not running. It is
	// installed before control passes in and taken back out afterwards.
	frames []*frame
	calls  []php.StackFrame
	scope  string
	called string
}

func asFiber(object *php.Object) (*fiber, bool) {
	if object == nil {
		return nil, false
	}
	held, ok := object.Internal.(*fiber)
	return held, ok
}

// fiberError is the exception every misuse of the API earns. The class is
// FiberError, which a program may catch but may not construct.
func fiberError(format string, args ...any) error {
	return Raise("FiberError", format, args...)
}

// fiberMethod implements the instance side of Fiber.
func (in *Interp) fiberMethod(ctx *php.Ctx, unit *Unit, call *pendingCall,
	depth int) (php.Value, bool, error) {
	held, ok := asFiber(call.this)
	if !ok {
		return php.Undef(), false, nil
	}
	argument := func(i int) php.Value {
		if i < len(call.args) {
			return call.args[i].Deref()
		}
		return php.Null()
	}
	switch strings.ToLower(call.builtinMethod) {
	case "__construct":
		if held.callable.Kind() != php.KindUndef {
			return php.Undef(), true, fiberError("Cannot call constructor twice")
		}
		if len(call.args) == 0 {
			return php.Undef(), true, Raise("ArgumentCountError",
				"Fiber::__construct() expects exactly 1 argument, 0 given")
		}
		if reason := in.callableReason(ctx, unit, call.args[0]); reason != "" {
			return php.Undef(), true, Raise("TypeError",
				"Fiber::__construct(): Argument #1 ($callback) must be a valid callback, %s", reason)
		}
		held.callable = call.args[0].Deref()
		// A fiber that is started and never finished is unwound at the end of
		// the request, so the unit has to know about it.
		unit.fibers = append(unit.fibers, held)
		return php.Null(), true, nil

	case "start":
		if held.started {
			return php.Undef(), true, fiberError("Cannot start a fiber that has already been started")
		}
		// A fiber needs a stack, and fiber.stack_size says how big. Left alone
		// it means the platform's default; set to anything smaller than two
		// pages -- including the empty string, which reads as zero -- there is
		// nowhere for the fiber to run.
		if unit.iniAssigned("fiber.stack_size") &&
			stdlib.IniQuantity(unit.setting("fiber.stack_size")) < minFiberStack {
			return php.Undef(), true, Raise("Exception",
				"Fiber stack size is too small, it needs to be at least %d bytes",
				minFiberStack)
		}
		if unit.fiberSwitchBlocked > 0 {
			return php.Undef(), true, fiberError("Cannot switch fibers in current execution context")
		}
		held.started = true
		held.toFiber = make(chan resumption)
		held.fromFip = make(chan suspension)
		args := append([]php.Value(nil), call.args...)
		go func() {
			// The first resumption is only a starting gun: the arguments were
			// fixed when start() was called. The callable's own frame is the
			// boundary a backtrace crosses, and callValue pushes it: PHP shows
			// it with no file or line, because the engine and not the script
			// made the call.
			<-held.toFiber
			value, err := in.callValue(ctx, unit, held.callable, args, depth)
			held.fromFip <- suspension{value: value, finished: true, err: err}
		}()
		return in.switchTo(ctx, unit, held, resumption{value: php.Null()})

	case "resume":
		if !held.suspended {
			return php.Undef(), true, fiberError("Cannot resume a fiber that is not suspended")
		}
		if unit.fiberSwitchBlocked > 0 {
			return php.Undef(), true, fiberError("Cannot switch fibers in current execution context")
		}
		return in.switchTo(ctx, unit, held, resumption{value: argument(0)})

	case "throw":
		if !held.suspended {
			return php.Undef(), true, fiberError("Cannot resume a fiber that is not suspended")
		}
		object := argument(0).Object()
		if object == nil {
			return php.Undef(), true, Raise("TypeError",
				"Fiber::throw(): Argument #1 ($exception) must be of type Throwable, %s given",
				givenTypeName(argument(0)))
		}
		if unit.fiberSwitchBlocked > 0 {
			return php.Undef(), true, fiberError("Cannot switch fibers in current execution context")
		}
		return in.switchTo(ctx, unit, held, resumption{throw: &thrown{object: object}})

	case "getreturn":
		switch {
		case held.failed:
			return php.Undef(), true, fiberError(
				"Cannot get fiber return value: The fiber threw an exception")
		case !held.terminated && !held.started:
			return php.Undef(), true, fiberError(
				"Cannot get fiber return value: The fiber has not been started")
		case !held.terminated:
			return php.Undef(), true, fiberError(
				"Cannot get fiber return value: The fiber has not returned")
		}
		return held.returned, true, nil

	case "isstarted":
		return php.Bool(held.started), true, nil
	case "issuspended":
		return php.Bool(held.suspended), true, nil
	case "isrunning":
		return php.Bool(held.running), true, nil
	case "isterminated":
		return php.Bool(held.terminated), true, nil
	}
	return php.Undef(), false, nil
}

// switchTo hands control to a fiber and waits for it to come back, which it
// does either by suspending or by finishing. The interpreter state each side
// owns is swapped across the handoff, so neither sees the other's frames.
func (in *Interp) switchTo(ctx *php.Ctx, unit *Unit, held *fiber,
	message resumption) (php.Value, bool, error) {
	// A fiber's frames sit *on top of* whoever resumed it rather than replacing
	// them: that is what a backtrace taken inside a fiber shows, all the way
	// down through `Fiber->resume()` to `{main}`. They are lifted back off
	// afterwards, so a backtrace taken while the fiber is suspended does not
	// see them.
	resumerFrames := unit.frames
	resumerCalls := ctx.SwapCallStack(nil)
	ctx.SwapCallStack(append(append([]php.StackFrame(nil), resumerCalls...), held.calls...))
	unit.frames = append(append([]*frame(nil), resumerFrames...), held.frames...)
	savedScope := ctx.EnterScope(held.scope)
	savedCalled := ctx.EnterCalledScope(held.called)
	savedFiber := unit.currentFiber
	unit.currentFiber = held
	savedReporting := ctx.ErrorReporting()
	if !held.reportingSet {
		held.reporting, held.reportingSet = ctx.UnsuppressedReporting(), true
	}
	ctx.SetErrorReporting(held.reporting)
	held.running, held.suspended = true, false

	held.toFiber <- message
	out := <-held.fromFip

	if combined := unit.frames; len(combined) > len(resumerFrames) {
		held.frames = append([]*frame(nil), combined[len(resumerFrames):]...)
	} else {
		held.frames = nil
	}
	if combined := ctx.SwapCallStack(resumerCalls); len(combined) > len(resumerCalls) {
		held.calls = append([]php.StackFrame(nil), combined[len(resumerCalls):]...)
	} else {
		held.calls = nil
	}
	held.scope = ctx.EnterScope(savedScope)
	held.called = ctx.EnterCalledScope(savedCalled)
	held.reporting = ctx.SetErrorReporting(savedReporting)
	unit.frames = resumerFrames
	unit.currentFiber = savedFiber
	held.running = false

	if !out.finished {
		held.suspended = true
		return out.value, true, nil
	}
	held.terminated = true
	held.frames, held.calls = nil, nil
	if out.err != nil {
		held.failed = true
		return php.Undef(), true, out.err
	}
	held.returned = out.value
	return php.Null(), true, nil
}

// fiberStaticCall answers Fiber::suspend and Fiber::getCurrent.
func (in *Interp) fiberStaticCall(ctx *php.Ctx, unit *Unit, method string,
	args []php.Value) (php.Value, bool, error) {
	switch strings.ToLower(method) {
	case "suspend":
		held := unit.currentFiber
		if held == nil {
			return php.Undef(), true, fiberError("Cannot suspend outside of a fiber")
		}
		if held.closing {
			// The unwind is already on its way out; there is nobody left to
			// resume this fiber, so suspending again cannot return.
			return php.Undef(), true, fiberError("Cannot suspend in a force-closed fiber")
		}
		if unit.fiberSwitchBlocked > 0 {
			return php.Undef(), true, fiberError("Cannot switch fibers in current execution context")
		}
		value := php.Null()
		if len(args) > 0 {
			value = args[0].Deref()
		}
		// This runs on the fiber's own goroutine: handing the value out blocks
		// here until somebody resumes, which is exactly what suspending means.
		held.fromFip <- suspension{value: value}
		message := <-held.toFiber
		if message.throw != nil {
			return php.Undef(), true, message.throw
		}
		return message.value, true, nil

	case "getcurrent":
		if unit.currentFiber == nil || unit.currentFiber.object == nil {
			return php.Null(), true, nil
		}
		return php.Obj(unit.currentFiber.object), true, nil
	}
	return php.Undef(), false, nil
}

// forceClose unwinds a fiber that was started, suspended and never resumed.
// PHP does this when the Fiber object is destroyed; php2go has no refcounts, so
// it happens when the request ends -- the same timing its destructors have.
func (in *Interp) forceClose(ctx *php.Ctx, unit *Unit, held *fiber) {
	if err := in.forceCloseFor(ctx, unit, held); err != nil {
		if escaped, isThrown := asThrown(err); isThrown {
			// Nobody is on the stack to catch it -- the request is ending --
			// so it is reported as uncaught.
			in.settleMessages(ctx, unit, escaped)
			ctx.Write(escaped.fatalErrorText())
		}
	}
}

// forceCloseFor is forceClose for a caller that is still on the stack and can
// carry what the unwind threw. A finally block that throws while the fiber is
// being abandoned belongs to the point the program lost the fiber, so a try
// open there catches it.
func (in *Interp) forceCloseFor(ctx *php.Ctx, unit *Unit, held *fiber) error {
	if !held.started || !held.suspended || held.terminated {
		return nil
	}
	held.closing = true
	_, _, err := in.switchTo(ctx, unit, held, resumption{throw: errFiberExit})
	held.closing = false
	if err == nil || errors.Is(err, errFiberExit) {
		// The unwind reaching the top of the fiber is the expected end, not a
		// failure: nothing was thrown that anyone could catch.
		held.failed = false
		return nil
	}
	return err
}

// closeFibers unwinds every fiber left suspended when the request ends.
func (in *Interp) closeFibers(ctx *php.Ctx, unit *Unit) {
	for _, held := range unit.fibers {
		in.forceClose(ctx, unit, held)
	}
}

// newFiber builds the state behind a Fiber object. The callable arrives through
// the constructor, which runs as an ordinary method call afterwards.
func newFiber(object *php.Object, _ []php.Value) {
	object.Internal = &fiber{object: object}
}
