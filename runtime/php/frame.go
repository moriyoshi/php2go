package php

import "errors"

// A resumable function -- a generator body, or a fiber body -- is entered many
// times and continues where it left off. `internal/lower`'s CPS pass rewrites
// such a function into a state machine: suspension becomes a `ret` that records
// where to resume, and resuming becomes an ordinary call that dispatches on the
// recorded state. Nothing underneath has to know suspension exists, which is
// what lets compiled code drop coroutines.
//
// This file is the runtime half of that: what the state machine carries between
// entries, and how it hands control back. The drivers that sit on top -- the
// PHP-visible `Generator` and `Fiber` -- are in resumable.go.
//
// # The representation decision
//
// The compiler deliberately left open whether a frame is a generated struct per
// function or a slice indexed by slot. It is a **generated struct**, and the
// choice is forced rather than preferred:
//
//   - The IR declares slot types, and `$__iterN: *iter` is a foreach iterator
//     that must not be boxed as a `zval`. A uniform `[]Value` cannot hold one
//     without boxing; a struct gives each slot its declared Go type for free.
//   - "Every declared slot must live as long as the frame does" becomes
//     structural. A field cannot be dropped after its last read, so a generator's
//     locals keep their destructor ordering without the emitter having to
//     promise anything.
//
// So `runtime/php` owns only the machinery every frame has -- the state number
// and the resumed value, below -- and the generated struct embeds it:
//
//	type counterFrame struct {
//	    php.FrameState
//	    rows  php.Value    // $rows
//	    seen  php.Value    // $seen
//	    iter5 *php.Iter    // $__iter5, not a zval
//	    ...
//	}
//
// Slot storage is generated, so it is not declared here. The measurement behind
// the choice is in the journal; frames are small (1.4 slots on `Zend/tests`, 18
// at the largest), which is what makes per-function structs cheap.

// OutcomeKind is how a resumable function came back. Each value is one of the
// terminators the CPS pass produces, and together they are the whole protocol
// between a driver and a state machine.
type OutcomeKind uint8

const (
	// Returned is `ret`: the function ran off its end or returned. Value is the
	// return value, which for a generator is what `getReturn()` answers.
	Returned OutcomeKind = iota
	// Created is `ret.generator`: the eager prologue finished. It separates
	// parameter binding and type checks, which run when the function is
	// *called*, from the body, which runs when it is first advanced. A
	// generator function's argument TypeError therefore fires before the
	// generator object exists, which is what the engine does.
	Created
	// Yielded is `ret.yield`: suspended with a value, and optionally a key.
	Yielded
	// Delegated is `ret.yield_from`: suspended handing off to another source,
	// whose elements are produced in this generator's place until it is spent.
	Delegated
	// Suspended is `ret.suspend`: a fiber gave control back to whoever resumed
	// it. `internal/lower` emits it -- 103 sites over 95 bodies in `Zend/tests`
	// -- because the suspending function's CPS form is the common prefix of
	// every option for fibers, so building it settles none of them. What stays
	// open is the *caller*, recorded on the function as `.suspends_to_caller`.
	//
	// Which driver sees this depends on what is above the body, and the two
	// cases are not alike:
	//
	//   - The Fiber driver is the body's only caller, so the suspension is
	//     already where it needs to be. That is the runnable case.
	//   - A generator body can also call `Fiber::suspend()` -- 26 of the 95 --
	//     and then the suspension has to travel out through the Generator
	//     driver and back in on resume, across whatever PHP frame called
	//     `$g->next()`. That needs the caller question answered, so the driver
	//     reports ErrSuspendAcrossDriver rather than guessing.
	Suspended
	// Called is `ret.call`: the function reached a call that may suspend, and
	// hands up the callee's frame rather than performing the call itself.
	//
	// It exists because `Fiber::suspend()` suspends the whole stack it runs on,
	// so a caller of a suspending function has to be able to give control up
	// too -- and a frame that performed the call with an ordinary Go call could
	// not, because the suspension would have to travel back through a Go return.
	// Handing the frame up instead keeps the stack **explicit**, which is the
	// whole reason the compiled side is CPS rather than goroutines: a backtrace
	// and a destructor order can be read off an explicit stack and cannot be
	// read off the Go one.
	//
	// It never reaches a driver. `callStack` follows it, and what a driver sees
	// is what the innermost frame produced.
	Called
)

func (k OutcomeKind) String() string {
	switch k {
	case Returned:
		return "returned"
	case Created:
		return "created"
	case Yielded:
		return "yielded"
	case Delegated:
		return "delegated"
	case Suspended:
		return "suspended"
	case Called:
		return "called"
	}
	return "unknown"
}

// Outcome is one return from a state machine.
type Outcome struct {
	Kind OutcomeKind
	// Key is the key of a `yield $k => $v`. Undef means the yield named none
	// and the driver supplies the next automatic one -- the counter is per
	// generator instance rather than per program point, so the IR does not and
	// should not carry it.
	Key Value
	// Value is what was yielded, delegated to, suspended with, or returned.
	Value Value
	// Frame is the callee's frame for a `Called`: built, not entered.
	Frame Resumable
}

// FrameState is the part of a frame that every resumable function has: where to
// resume, and what the last resume handed in. A generated frame embeds it; the
// declared slots are that struct's own fields.
type FrameState struct {
	state   int
	resumed Value
	// pending is an exception to raise *at the suspension point* rather than a
	// value to hand back -- `Generator::throw()` and `Fiber::throw()`. It has to
	// surface inside the function, not in the driver, or the function's own
	// `try` around the `yield` could not catch it.
	pending error
}

// State is `frame.state`: the dispatch selector at the function's entry.
func (f *FrameState) State() int { return f.state }

// SetState is `frame.set_state`, emitted just before a suspending terminator.
func (f *FrameState) SetState(n int) { f.state = n }

// Resume records the value the next entry will see, and clears any pending
// throw.
func (f *FrameState) Resume(v Value) {
	f.resumed, f.pending = v, nil
}

// ResumeThrowing records that the next entry must raise err at the point it
// resumes rather than continue with a value.
func (f *FrameState) ResumeThrowing(err error) {
	f.resumed, f.pending = Undef(), err
}

// Resumed is `frame.resumed`. It reports the pending throw once, so the
// generated code's natural shape --
//
//	v, err := fr.Resumed()
//	if err != nil { /* unwind, inside this block's region */ }
//
// -- puts the exception at the suspension point, where the function's own
// handlers can see it.
//
// Every continuation calls this, including one that discards the resumed value:
// the read is not there to produce a value, it is the only program point at
// which a thrown-in exception can surface. `internal/lower` emits it in all of
// them, checked by its own TestEveryContinuationReadsTheResumedValue.
// Reporting the pending throw *once* and clearing it is what makes a second read
// safe. A continuation that is also an ordinary join -- `counter`'s bb6 -- is
// entered on paths that did not suspend, and reads again; taking the effect once
// means the re-read cannot raise an exception a second time. The value being
// stale on that path is harmless separately, and checked: no continuation that
// *uses* its resumed value has a non-suspension predecessor, asserted over both
// corpora by `php2lower -cps`.
func (f *FrameState) Resumed() (Value, error) {
	if err := f.pending; err != nil {
		f.pending = nil
		return Undef(), err
	}
	return f.resumed, nil
}

// Resumable is one instance of a resumable function: a frame, plus the state
// machine that reads it. The generated struct supplies Step; the rest comes
// from the embedded FrameState.
//
// The interface is deliberately narrow. A driver only ever sets what the next
// entry sees and calls it; State, SetState and Resumed belong to the generated
// body, which has the concrete type.
//
// # The one obligation left on a driver
//
// **Do not call Step again after it returns an error.** A body that returns is
// safe: `internal/lower` gives every function a terminal state, so each `ret`
// parks its value in `$__ret`, records that state, and the terminal block is
// `ret $__ret` and nothing else. Re-entry after a return is idempotent, and
// checked by execution rather than read off the IR -- against generated code,
// `counter(3)` returns 3, then 3, then 3.
//
// A body that *threw* is a different matter and the guarantee does not reach it:
// the state is still whichever continuation raised, so re-entering resumes the
// body as though nothing had happened. Against the same generated frame,
// throwing into `counter` at its first yield and stepping again **yields 10**.
//
// Both drivers here guard it -- Generator via `finished` (set by `abort`), Fiber
// via FiberTerminated -- so nothing reaches it. It is written down because it is
// still accidental on the throw path: neither the IR nor the emitted code
// enforces it, so a third driver would get it wrong silently. The remedy is the
// same as the one that fixed the return path, applied to the throwing one.
type Resumable interface {
	Resume(Value)
	ResumeThrowing(error)
	Step(ctx *Ctx) (Outcome, error)
}

// The conditions a driver refuses, as sentinels. `runtime/php` has no vocabulary
// for PHP exception classes -- constructing one needs a class table, which lives
// above this package -- so a driver reports the condition and the layer that
// knows about classes maps it to Error, Exception or FiberError. Mapping a
// sentinel is unambiguous in a way that matching a message is not.
var (
	// ErrGeneratorRunning is `Cannot resume an already running generator`.
	ErrGeneratorRunning = errors.New("php: generator is already running")
	// ErrGeneratorNotReturned is `Cannot get return value of a generator that
	// hasn't returned`.
	ErrGeneratorNotReturned = errors.New("php: generator has not returned")
	// ErrGeneratorAlreadyRun is `Cannot rewind a generator that was already
	// run`.
	ErrGeneratorAlreadyRun = errors.New("php: generator was already advanced")
	// ErrGeneratorClosed is `Cannot traverse an already closed generator`,
	// which is **not** the same refusal as ErrGeneratorAlreadyRun even though
	// both come of using a generator too late. Asked of the engine: opening a
	// `foreach` over a *finished* generator is `Cannot traverse an already
	// closed generator`, while calling `rewind()` on the same object is
	// `Cannot rewind a generator that was already run`. The operation picks the
	// message, not the state.
	ErrGeneratorClosed = errors.New("php: generator is closed")
	// ErrYieldFromSelf is `Impossible to yield from the Generator being
	// currently run`. Without the guard this is not a wrong answer but a
	// **stack overflow**: the delegate resumes the generator that is delegating
	// to it, forever.
	ErrYieldFromSelf = errors.New("php: yield from the running generator")
	// ErrNotDelegatable is `yield from 5` -- the program handing `yield from`
	// something that is not an array or a Traversable. It is a **PHP error**
	// and not a gap: the engine throws `Error` with `Can use "yield from" only
	// with arrays and Traversables`. Kept apart from ErrDelegateUnsupported
	// because conflating them would file a program's mistake as our missing
	// capability, and the two want opposite responses.
	ErrNotDelegatable = errors.New(`php: can use "yield from" only with arrays and Traversables`)
	// ErrDelegateUnsupported marks a `yield from` over something no Delegate
	// covers yet. It is an honest gap, not a PHP error: reporting it as one
	// would be the gap-masquerading-as-behaviour QUALITY_GATE.md forbids.
	ErrDelegateUnsupported = errors.New("php: yield from over an unsupported source")
	// ErrIterUnsupported marks a `foreach` over something Iter cannot walk yet:
	// a Traversable, or a plain object whose visible properties depend on a
	// calling scope this package cannot see. An honest gap, for the same reason
	// as ErrDelegateUnsupported -- an empty iterator would make the loop
	// silently produce nothing.
	ErrIterUnsupported = errors.New("php: foreach over an unsupported source")
	// ErrNoObjectProperties marks a `var_dump` of an object with no
	// ObjectProperties hook installed. Rendering it without its properties
	// would be wrong output rather than a missing feature.
	ErrNoObjectProperties = errors.New("php: no object property source installed")
	// ErrNoCallValue marks a builtin that needs to invoke a PHP callable with
	// no CallValue hook installed. Skipping the callback would produce a
	// plausible wrong answer rather than a visible gap.
	ErrNoCallValue = errors.New("php: no way to call a PHP callable installed")
	// ErrSuspendAcrossDriver marks a `Fiber::suspend()` reached from inside a
	// generator body. It is a gap rather than a PHP error and rather than a
	// protocol violation: the IR is right, the emitter is right, and what is
	// missing is a resumable form for the PHP frame that called `$g->next()`.
	// See the Suspended comment above.
	ErrSuspendAcrossDriver = errors.New("php: a fiber suspension crossed a generator driver")

	// ErrFiberAlreadyStarted is `Cannot start a fiber that has already been
	// started`.
	ErrFiberAlreadyStarted = errors.New("php: fiber was already started")
	// ErrFiberNotSuspended is `Cannot resume a fiber that is not suspended`.
	ErrFiberNotSuspended = errors.New("php: fiber is not suspended")
	// The three ways `$f->getReturn()` is refused. PHP words each differently --
	// `Cannot get fiber return value: The fiber {has not been started, has not
	// returned, threw an exception}` -- so they are three sentinels rather than
	// one. A generator, by contrast, gives the same message for all of them,
	// which is why ErrGeneratorNotReturned stands alone. Settled by running the
	// cases, not by reading the source.
	ErrFiberNotStarted  = errors.New("php: fiber has not been started")
	ErrFiberNotReturned = errors.New("php: fiber has not returned")
	ErrFiberThrew       = errors.New("php: fiber threw an exception")
	// ErrFiberSuspendClosed is a `Fiber::suspend()` reached while the fiber
	// is being force-closed: `Cannot suspend in a force-closed fiber`.
	ErrFiberSuspendClosed = errors.New("php: fiber suspended while force-closed")
	// ErrFiberSwitchBlocked is starting, suspending, resuming or throwing
	// into a fiber where the engine blocks switching -- inside a tick
	// function (zend_fiber_switch_block around ZEND_TICKS).
	ErrFiberSwitchBlocked = errors.New("php: fiber switch blocked")
	// ErrProtocol marks a state machine that came back in a way its driver
	// cannot be asked to make sense of -- a generator body whose first entry
	// did not produce Created, say. It means the emitter and this file
	// disagree, so it should never reach a PHP program.
	ErrProtocol = errors.New("php: resumable function broke the driver protocol")
)
