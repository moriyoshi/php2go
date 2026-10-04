package php

import (
	"errors"
	"fmt"
)

// The drivers: the PHP-visible objects that sit on a state machine and turn
// method calls into entries into it. Neither is a coroutine. `resume` sets what
// the next entry will see and calls the function; the function comes back
// through one of the terminators in frame.go.
//
// This is for **compiled** code. `runtime/interp` executes op_arrays and keeps
// its goroutine fibers; nothing here replaces them.
//
// # What is settled and what is not
//
// The CPS pass rewrites *any* suspending body, fiber or generator. That was not
// always so, and the earlier split -- generators rewritten, fibers refused --
// was aimed at the wrong half of the problem. Rewriting the body that suspends
// is the common prefix of every option for fibers (uniform, dual entry,
// selective, keeping goroutines), so doing it commits to none of them. What is
// undecided is the **caller**, and the IR says so per function with
// `.suspends_to_caller` instead of refusing to lower.
//
// So the Fiber driver below is real, and its scope is exactly what the marker
// implies:
//
//   - A fiber whose suspensions are in the callable the driver itself steps.
//     The driver is the only caller, so nothing has to escape. Runnable.
//   - A suspension from deeper in the call stack. The emitter's rule -- never
//     emit a direct call to a function carrying `.suspends_to_caller` -- keeps
//     this from reaching the driver at all, which is the right place to stop
//     it: at the refusal to compile, not at a runtime pretence.
//
// # Why the caller question is hard
//
// The reason is a **cost**, not an impossibility, and the distinction matters
// because it decides whether this can ever change.
//
// The sharpest form of it: there is no *static set* of functions needing CPS to
// compute. Whether a frame lies inside a fiber's span is a property of the
// **activation**, not of the function -- the same function is on the fiber stack
// in one call and not in another. So it is not that the analysis is unobtainable
// over dynamic dispatch; it is that there is nothing for an analysis to find.
// A *uniform* rewrite sidesteps it by never asking: if every function takes a
// continuation then every call is already a suspension point and there is no
// native stack to lose. It is sound, and it costs a heap frame per call and a
// dispatch per entry everywhere, which is most of what compiling was for.
//
// The same activation argument rescues a two-version scheme, which is why one is
// still open: a **dynamic** "are we in a fiber" flag decides at each call site
// with no analysis at all, at roughly double the code size, since both versions
// have to exist everywhere precisely because the flag is dynamic.
//
// The one genuine limit is narrower than either: suspending across a **native**
// frame -- `Fiber::suspend()` inside a `usort` comparator -- which no PHP-level
// rewrite reifies, and which PHP's own fibers avoid by switching real stacks.
//
// # The cases the marker does not describe
//
// A generator body may itself call `Fiber::suspend()`: 26 of the 95 marked
// bodies in `Zend/tests` are generators, `gh15108-004.phpt` among them. Nothing
// calls those directly -- calling one returns a Generator object -- so the
// emitter's direct-call rule never fires for them, and the suspension escapes
// through a *driver* call instead.
//
// Two things follow that the marker on its own does not say, both settled
// against the engine rather than reasoned about:
//
//   - **Every entry point is an escape route, not just next/send.** `current()`,
//     `valid()`, `key()` and `rewind()` all prime the body, and a `foreach` over
//     a generator is a driver call spelled as iteration opcodes rather than as a
//     method name at all.
//   - **The marker does not survive `yield from`.** A generator that delegates
//     to a marked one carries no marker itself -- it contains no literal
//     `Fiber::suspend()` -- yet a driver call on *it* suspends the fiber, one
//     level out. That is `gh15108-004`'s own shape: `gen2($gen)` takes its
//     delegate as a parameter, so which generator it is is a value and not a
//     type.
//
// So the driver has to treat a suspension arriving from a delegate the same way
// it treats one from its own body: report ErrSuspendAcrossDriver and stay
// parked. Aborting would discard the whole `yield from` chain, and resuming any
// of it needs the caller decision.

// maxDelegateSteps bounds one advance of a generator. A `yield from` over a
// spent source resumes the body and steps again, so a chain of them makes one
// advance take several steps; only a cycle would make it take unboundedly many,
// and a cycle is what this stops.
const maxDelegateSteps = 1000

// Generator is PHP's Generator over a resumable function.
//
// It is created by the *call*, not by the first advance: a generator function
// splits into an eager prologue that binds and type-checks parameters and a
// resumable body, and the split is observable -- `g("bad")` on `function g(int
// $x)` throws before the generator object exists.
type Generator struct {
	body Resumable

	// Function names the body this generator runs. It is **output**, not
	// bookkeeping: `var_dump` of a Generator shows `["function"]=>` and shows
	// nothing else, so a generator with no name here dumps as `(0) {}`.
	Function string

	// Args are the arguments the generator function was **called** with, which
	// a trace renders on the body's frame: `#0 file(7): gen(Object(stdClass))`.
	// They belong to the generator rather than to any one resume, because the
	// frame is pushed fresh on every resume and names the original call each
	// time. Output only, like Function.
	Args []Value

	// ByRef is `function &gen()`, which is what decides whether
	// `foreach ($gen as &$v)` is allowed at all: a generator that did not
	// declare it has no slot to hand out, and PHP refuses rather than quietly
	// copying.
	ByRef bool

	// started is "the body has been entered past the prologue at least once".
	// PHP does not run any of it until the generator is first asked for
	// something.
	started bool
	// advanced is "past the first yield", which is the only thing rewind()
	// refuses.
	advanced bool
	running  bool
	finished bool
	// parked is a body stopped by a fiber suspension that a `GeneratorDrive` can
	// carry out to the fiber and resume later. It is the same condition as
	// `stuck`, told apart by whether anybody is in a position to resume it: a
	// driver call made from a frame that is itself resumable can, and an
	// ordinary one cannot.
	parked bool
	// suspendedWith is the value the parked body suspended with, which is what
	// the fiber's `start()` or `resume()` answers.
	suspendedWith Value
	// carried is a park a `GeneratorDrive` handed out to a fiber: that fiber
	// will resume it, so until then the generator is running (heldByFiber).
	// A park no drive carried is the gap, and stays reported as one.
	carried bool
	// stuck is a body that suspended a fiber from inside itself. Neither
	// finished nor advanceable; see ErrSuspendAcrossDriver.
	stuck bool
	// returned distinguishes "ran off its end" from "threw". Both finish the
	// generator, but only the first gives getReturn() an answer -- a body that
	// threw never returned, so the question stays refused.
	returned bool

	key     Value
	current Value
	ret     Value

	// autoKey is the next key a `yield $v` with no key of its own gets. It
	// belongs to the instance, which is why the IR does not carry it.
	autoKey int64

	// delegate is the source a `yield from` is currently pulling through.
	delegate Delegate
	// moves counts the times this generator yielded. leaf is the generator
	// whose yield this one last copied through its `yield from` chain, and
	// leafMoves that one's count then: a difference means someone else
	// advanced it since -- another generator delegating to it, directly or
	// through intermediates -- and the copy is stale (peekCurrent).
	moves, leafMoves uint64
	// threw is a body that ended by throwing: it has no return value, which
	// a `yield from` over it has to be told (ClosedGeneratorException).
	threw bool
	leaf  *Generator
	// closing is a generator being force-closed (Close): its finally blocks
	// run, and a `yield` or `yield from` reached there is refused with an
	// Error thrown at it instead of suspending a body nobody will resume.
	closing bool
	// soleDelegate is a generator whose only holder is the `yield from`
	// that delegates to it -- `yield from inner()` -- so destroying the
	// delegator destroys it first (OwnDelegate).
	soleDelegate bool
	// fromFile and fromLine are where the body's `yield from` is, recorded
	// when it began delegating: the delegate's frame names this line.
	fromFile string
	fromLine uint32
	// hidden is the generator newIteratorDelegate makes to drive an Iterator
	// whose methods may suspend. It is not a PHP generator, so it has no
	// frame of its own in a backtrace: only the methods it calls do.
	hidden bool
	// self is the Generator object this drives, for a walk that reaches the
	// generator through a `yield from` rather than through the object
	// (GCVisit).
	self *Object
}

// StartGenerator runs a generator function's eager prologue and wraps what is
// left. An error here is the prologue's -- a parameter type check, say -- and it
// belongs to the call site, with no generator object made.
func StartGenerator(ctx *Ctx, body Resumable) (*Generator, error) {
	out, err := body.Step(ctx)
	if err != nil {
		return nil, err
	}
	if out.Kind != Created {
		return nil, fmt.Errorf("%w: a generator body's first entry gave %s, not created",
			ErrProtocol, out.Kind)
	}
	// Wrapped for the same reason a fiber's body is: a generator body can call
	// a function that suspends, and the call has to be followed rather than
	// surfacing here. Note this does **not** make a fiber suspension crossing a
	// generator driver work -- that still needs the frame that called
	// `$g->next()` to be resumable, and is `ErrSuspendAcrossDriver` below.
	cs := newCallStack(body, false)
	// The prologue's position is where the body's first entry resumes.
	cs.atFile, cs.atLine = ctx.Location()
	g := &Generator{body: cs, ret: Null(), key: Null(), current: Null()}
	ctx.trackGenerator(g)
	return g, nil
}

// trackGenerator remembers a generator for the end of the request. The list
// is pruned of finished ones as it grows, so a program that makes many and
// runs them out holds on to none of them.
func (c *Ctx) trackGenerator(g *Generator) {
	if c == nil {
		return
	}
	if n := len(c.generators); n >= 1024 && n&(n-1) == 0 {
		kept := c.generators[:0]
		for _, h := range c.generators {
			if !h.finished {
				kept = append(kept, h)
			}
		}
		c.generators = kept
	}
	c.generators = append(c.generators, g)
}

// CloseLiveGenerators closes, in the order they were made, the generators
// still suspended when the request ends: the engine destroys every object
// then, and a suspended generator's destruction runs its finally blocks --
// one left in a variable (generators/finally/finally_ran_on_close), or
// held by a closure (run_on_dtor). The first throw from one is returned.
func (c *Ctx) CloseLiveGenerators() error {
	var first error
	// held are the generators a suspended fiber has on its stack. They are
	// not closed on their own: the fiber's close below unwinds them, the
	// innermost first, as part of unwinding the fiber (gh9916-003).
	var held []*Generator
	for len(c.generators) > 0 {
		live := c.generators
		c.generators = nil
		for _, g := range live {
			if g.heldByFiber() {
				held = append(held, g)
				continue
			}
			// The engine destroys it, not a line of the script: its frame
			// traces from `[internal function]` (as RunPendingDestructors).
			c.SetLocation(NoActiveFile, 0)
			if err := g.Close(c); err != nil && first == nil {
				first = err
			}
		}
	}
	// **A fiber still suspended is force-closed too**, its finally blocks
	// run (suspend-in-force-close-fiber-after-shutdown).
	for len(c.fibers) > 0 {
		live := c.fibers
		c.fibers = nil
		for _, f := range live {
			if err := f.Close(c); err != nil && first == nil {
				first = err
			}
		}
	}
	// The ones the fibers' unwinds left behind -- a generator delegating to
	// one that stopped at a yield -- are destroyed now, like any other. One
	// still parked is the gap, reported as one rather than dropped.
	for _, g := range held {
		if g.heldByFiber() {
			if first == nil {
				first = UnsupportedFor(ErrSuspendAcrossDriver,
					"closing a generator a suspended fiber holds")
			}
			continue
		}
		c.SetLocation(NoActiveFile, 0)
		if err := g.Close(c); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Valid is `$g->valid()`.
func (g *Generator) Valid(ctx *Ctx) (bool, error) {
	if err := g.prime(ctx); err != nil {
		return false, err
	}
	return !g.finished, nil
}

// Current is `$g->current()`, and NULL once the generator is finished. While a
// `yield from` is in progress it reads *through* the delegate rather than from
// a copy: two generators delegating to one source both have to see where that
// source is now.
func (g *Generator) Current(ctx *Ctx) (Value, error) {
	if err := g.prime(ctx); err != nil {
		return Null(), err
	}
	// **Asking a delegator whose delegate was aborted resumes it** with
	// ClosedGeneratorException at its `yield from`: current() is where the
	// engine finds out, not only next() (mutli_yield_from_with_exception).
	if inner := delegatedGenerator(g.delegate); inner != nil && inner.threw && !g.finished {
		if err := g.pull(ctx); err != nil {
			return Null(), err
		}
	}
	return g.peekCurrent(ctx)
}

// peekCurrent answers without entering the body. While a `yield from` is in
// progress it reads *through* the delegate rather than from a copy, and a
// delegate that is itself delegating recurses -- two generators pulling from one
// source both have to see where that source is now.
// snapDelegate copies the delegate's element into this generator.
//
// The engine **copies**, and reading through the delegate instead is only
// equivalent while this generator is the one advancing it. Two generators can
// delegate to the same one, and it can be advanced directly besides:
//
//	$g1 = g1();  $g2 = g2($g1);   // g2 does `yield from $g1`
//	$g2->current();               // int(1)
//	$g1->next();                  // $g1 is now spent
//	$g2->current();               // int(1) still -- not NULL
//
// Reading through answered NULL there, because the shared delegate had
// finished. What `$g2` yields is what it took when it took it.
func (g *Generator) snapDelegate(ctx *Ctx) error {
	v, err := g.delegate.Current(ctx)
	if err != nil {
		return err
	}
	k, err := g.delegate.Key(ctx)
	if err != nil {
		return err
	}
	g.current, g.key = v, k
	g.leaf = nil
	if inner := delegatedGenerator(g.delegate); inner != nil {
		g.leaf = inner.producer()
		g.leafMoves = g.leaf.moves
	}
	return nil
}

// producer is the generator whose own yield this one currently presents:
// itself, or the far end of its `yield from` chain.
func (g *Generator) producer() *Generator {
	if g.leaf != nil && g.delegate != nil {
		return g.leaf
	}
	return g
}

func (g *Generator) peekCurrent(ctx *Ctx) (Value, error) {
	if g.finished {
		return Null(), nil
	}
	// **Through a delegate that is still running**, not from the copy: two
	// generators delegating to one both see it advance, whichever of them
	// advanced it (multiple_yield_from_on_same_generator). Only once it is
	// spent does the copy answer -- the case snapDelegate describes.
	if inner := g.liveDelegate(); inner != nil && g.stale() {
		return inner.peekCurrent(ctx)
	}
	return g.current, nil
}

// closedDelegateErr is the ClosedGeneratorException a delegator is thrown
// when the generator it yields from was aborted: made inside this
// generator's frame, at its `yield from` line, as the engine makes it.
func (g *Generator) closedDelegateErr(ctx *Ctx) error {
	file, line := ctx.Location()
	ctx.PushCall(StackFrame{Function: g.Function, File: file, Line: line, Args: g.Args})
	if g.fromFile != "" {
		ctx.SetLocation(g.fromFile, g.fromLine)
	}
	err := RaiseAt(ctx, "ClosedGeneratorException", "%s",
		"Generator yielded from aborted, no return value available")
	ctx.PopCall()
	return err
}

// stale reports that the generator this one's copy came from has yielded
// again since -- advanced by someone else.
func (g *Generator) stale() bool {
	// A spent leaf is not what a live chain presents any more: whichever
	// generator it returned to has moved on (only the direct delegate being
	// spent keeps the copy, and liveDelegate answers that first).
	if g.leaf == nil || g.leaf.moves != g.leafMoves || g.leaf.finished {
		return true
	}
	// The delegate may have stopped presenting that one: its own `yield
	// from` ended and it yields for itself again.
	if inner := g.liveDelegate(); inner != nil && inner.producer() != g.leaf {
		return true
	}
	return false
}

// liveDelegate is the generator a `yield from` is still pulling from, or nil
// once it is spent or when the source is not a generator.
func (g *Generator) liveDelegate() *Generator {
	if g.delegate == nil {
		return nil
	}
	if _, direct := g.delegate.(*generatorDelegate); !direct {
		return nil
	}
	inner := delegatedGenerator(g.delegate)
	if inner == nil || inner == g || inner.finished {
		return nil
	}
	return inner
}

// CurrentRef is the box a `function &gen()` yielded, for `foreach ($gen as
// &$v)` to bind to: writing `$v` writes the generator's variable. A value
// yielded with nothing behind it has no box, and the loop gets the value.
func (g *Generator) CurrentRef() (*Ref, bool) {
	if g == nil || g.finished || g.delegate != nil {
		return nil, false
	}
	return g.current.RefOf()
}

// Key is `$g->key()`.
func (g *Generator) Key(ctx *Ctx) (Value, error) {
	if err := g.prime(ctx); err != nil {
		return Null(), err
	}
	return g.peekKey(ctx)
}

// peekKey is peekCurrent for the key.
func (g *Generator) peekKey(ctx *Ctx) (Value, error) {
	if g.finished {
		return Null(), nil
	}
	if inner := g.liveDelegate(); inner != nil && g.stale() {
		return inner.peekKey(ctx)
	}
	return g.key, nil
}

// Next is `$g->next()`: resume with NULL.
func (g *Generator) Next(ctx *Ctx) error {
	if err := g.prime(ctx); err != nil {
		return err
	}
	return g.resume(ctx, Null())
}

// Send is `$g->send($v)`. On a generator that has not run yet it primes first
// and *then* sends, so the value reaches the first `yield`'s result rather than
// being dropped on the way to it.
func (g *Generator) Send(ctx *Ctx, v Value) (Value, error) {
	if err := g.prime(ctx); err != nil {
		return Null(), err
	}
	if err := g.resume(ctx, v); err != nil {
		return Null(), err
	}
	return g.Current(ctx)
}

// Throw is `$g->throw($e)`: resume by raising at the suspension point, so the
// generator's own `try` around the `yield` can catch it. A `yield from` sends it
// to the generator actually suspended, which is the innermost of the chain.
func (g *Generator) Throw(ctx *Ctx, thrown error) (Value, error) {
	if err := g.prime(ctx); err != nil {
		return Null(), err
	}
	if g.finished {
		return Null(), thrown
	}
	// The exception handed in is dropped: throwing into a running generator
	// is refused before anything reaches its body (gh19326).
	if g.heldByFiber() {
		return Null(), ErrGeneratorRunning
	}
	if inner, ok := g.delegate.(*generatorDelegate); ok {
		if _, err := inner.g.Throw(ctx, thrown); err != nil {
			// The delegate did not handle it, so the exception **continues
			// into this generator's body** at the `yield from` -- where a
			// try/catch around the delegation is entitled to catch it. Handing
			// it straight back to the caller skipped that, and left this
			// generator neither finished nor resumed: it went on answering the
			// element it had taken before the throw.
			g.delegate = nil
			g.body.ResumeThrowing(err)
			if err := g.step(ctx); err != nil {
				return Null(), err
			}
			return g.Current(ctx)
		}
		if err := g.pull(ctx); err != nil {
			return Null(), err
		}
		return g.Current(ctx)
	}
	g.delegate = nil
	g.body.ResumeThrowing(thrown)
	if err := g.step(ctx); err != nil {
		return Null(), err
	}
	return g.Current(ctx)
}

// ErrGeneratorClose unwinds a generator that was started, suspended, and will
// never be resumed. It is **not an exception**: nothing can catch it, and no
// `catch` clause matches it because it is not a PHP object at all -- but every
// `finally` on the way out runs, which is the only reason PHP resumes an
// abandoned generator.
var ErrGeneratorClose = errors.New("php: generator close")

// GeneratorCloseClass is the class name the close unwind wears while it travels
// through a compiled body's handler machinery. No program can declare it -- the
// `@` is not valid in a PHP class name -- so no `catch` clause and no
// `instanceof` can match it, which is what makes the unwind run every `finally`
// and be caught by nothing.
const GeneratorCloseClass = "generator@close"

// Close runs the finally blocks of a generator nobody can reach any more.
//
// PHP does this when the Generator object is destroyed, and the timing is
// observable: a generator held only by a `foreach` runs its finally when the
// loop ends, one held in a variable when that variable is unset, and one held
// to the end of the script at shutdown. This performs the unwind; **when** to
// perform it is the caller's question, because only the caller knows who still
// holds the generator.
//
// A generator that never started, already finished, or is currently running has
// nothing to unwind.
func (g *Generator) Close(ctx *Ctx) error {
	if g == nil || !g.started || g.finished || g.running {
		return nil
	}
	// **One a suspended fiber holds is not this close's to unwind**: its
	// body is on the fiber's stack, and the engine unwinds it as part of
	// destroying the fiber. The gap is reported as it was before the
	// running-generator refusal existed -- not as `Cannot resume an already
	// running generator`, which the engine never says here.
	if g.heldByFiber() {
		return UnsupportedFor(ErrSuspendAcrossDriver,
			"closing a generator a suspended fiber holds")
	}
	// **Destroying a delegator leaves its delegate alone**: the generator a
	// `yield from` pulls through may be held elsewhere, and the engine only
	// detaches it -- `yield_from($gen)->current()` twice reads $gen both
	// times (repeated_yield_from_with_immediate_release). Throwing the close
	// through the delegate aborted it.
	// Unless the delegator was its only holder: the engine releases the
	// delegate **before** running the delegator's finally blocks, so one
	// nothing else holds is closed first (bug71601: `Inner finally`, then
	// `Finally`). A throw from it still lets the delegator's finally run.
	var innerErr error
	if inner, ok := g.delegate.(*generatorDelegate); ok && inner.g.soleDelegate {
		innerErr = inner.g.Close(ctx)
	}
	g.delegate = nil // detach before the close unwind
	g.closing = true
	_, err := g.Throw(ctx, ErrGeneratorClose)
	g.finished = true
	// The close may come back dressed as the object compiled code throws it
	// as; either way it is the expected end.
	if err == nil || isGeneratorClose(err) {
		// Reaching the top of the body is the expected end of the unwind.
		return innerErr
	}
	// A `finally` that threw during the unwind replaces whatever abandoned the
	// generator, which is what the engine does too.
	return err
}

// Rewind is `$g->rewind()`: prime, and refuse only if the generator is already
// past its first yield.
func (g *Generator) Rewind(ctx *Ctx) error {
	if err := g.prime(ctx); err != nil {
		return err
	}
	if g.advanced {
		return ErrGeneratorAlreadyRun
	}
	return nil
}

// GetReturn is `$g->getReturn()`, which is a question a generator that has not
// finished cannot answer.
//
// It **primes** first, which is the part that is easy to miss: the engine calls
// this "auto-priming", and it matters for a body that returns before it ever
// yields. `function g() { return 42; yield 24; }` has returned by the time it
// first suspends -- which is never -- so `g()->getReturn()` is `int(42)` on an
// untouched generator, where refusing without starting it made that
// `Cannot get return value of a generator that hasn't returned`. Priming is not
// advancing: a generator already suspended at a yield stays there and is still
// refused.
func (g *Generator) GetReturn(ctx *Ctx) (Value, error) {
	if err := g.prime(ctx); err != nil {
		return Null(), err
	}
	return g.returnValue()
}

// returnValue is GetReturn without the priming, for the callers that already
// know the body has finished.
func (g *Generator) returnValue() (Value, error) {
	if !g.returned {
		return Null(), ErrGeneratorNotReturned
	}
	return g.ret, nil
}

// Finished reports whether the body has run to completion. It is the driver's
// own state, asked without entering the body -- unlike Valid, which primes.
func (g *Generator) Finished() bool { return g.finished }

// YieldsByRef reports whether the body was declared `function &gen()`.
func (g *Generator) YieldsByRef() bool { return g != nil && g.ByRef }

// prime runs the body up to its first suspension, once.
func (g *Generator) prime(ctx *Ctx) error {
	// **Parked before its first yield, it is still being primed**, and the
	// engine's priming of it from anywhere else -- current(), key(),
	// valid(), rewind() -- is a resume of a running generator: `Cannot
	// resume an already running generator` (probed). Once it has yielded,
	// those read the element it holds and need not enter it.
	if g.parked && g.carried && g.moves == 0 && g.delegate == nil {
		return ErrGeneratorRunning
	}
	if g.started {
		return nil
	}
	g.started = true
	return g.step(ctx)
}

// resume hands a value back to whatever is suspended -- the delegate if one is
// in progress, the body otherwise -- and advances.
func (g *Generator) resume(ctx *Ctx, sent Value) error {
	if g.finished {
		return nil
	}
	if g.heldByFiber() {
		return ErrGeneratorRunning
	}
	g.advanced = true
	asDelegate := ctx.takeStepAsDelegate()
	if g.delegate != nil {
		// **This generator's frame lies under the delegate's**, as the
		// engine's does: the delegate's frame names the `yield from` line,
		// and this one the driver -- `#0 file(14): inner()`, `#1 file(17):
		// outer()` -- where the delegate ran on the driver's frame alone
		// (mutli_yield_from_with_exception).
		file, line := ctx.Location()
		ctx.PushCall(StackFrame{Function: g.Function, File: file, Line: line, Args: g.Args, Delegate: asDelegate})
		if g.fromFile != "" {
			ctx.SetLocation(g.fromFile, g.fromLine)
		}
		ctx.stepAsDelegate = delegatedGenerator(g.delegate) != nil
		err := g.delegate.Advance(ctx, sent)
		ctx.stepAsDelegate = false
		ctx.PopCall()
		if err != nil {
			// A suspension inside the delegate is not the delegate failing, and
			// it travels *up* the `yield from` chain: this generator is parked
			// behind a parked one, and neither is finished. Aborting here would
			// throw away the whole chain, which is the same mistake as aborting
			// the innermost body -- one level out, and reached by a driver call
			// on a generator that carries no marker of its own.
			if errors.Is(err, ErrSuspendAcrossDriver) {
				// **Parked one level out.** The generator this one delegates to
				// stopped on a fiber suspension, so this one is stopped too --
				// and a `GeneratorDrive` above can carry that suspension to the
				// fiber and resume the chain. `parkedInChain` finds the body
				// that actually stopped; the value it suspended with is the
				// fiber's answer, and it is this one's too.
				if p := g.parkedInChain(); p != nil {
					g.parked, g.suspendedWith = true, p.suspendedWith
					return err
				}
				g.stuck = true
				return err
			}
			// The source failed with an exception: it continues into this
			// generator's body at the `yield from`, as `Throw` does.
			if isPHPException(err) {
				g.delegate = nil
				g.body.ResumeThrowing(err)
				ctx.stepAsDelegate = asDelegate
				return g.step(ctx)
			}
			g.abort()
			return err
		}
		ctx.stepAsDelegate = asDelegate
		err = g.pull(ctx)
		ctx.stepAsDelegate = false
		return err
	}
	g.body.Resume(sent)
	ctx.stepAsDelegate = asDelegate
	return g.step(ctx)
}

// takeStepAsDelegate answers whether the generator being entered is stepped
// by the one yielding from it, and clears the mark so nothing entered later
// inherits it.
func (c *Ctx) takeStepAsDelegate() bool {
	as := c.stepAsDelegate
	c.stepAsDelegate = false
	return as
}

// isGeneratorValue reports a Generator object, which a `yield from` primes
// as its delegate.
func isGeneratorValue(v Value) bool {
	v = v.Deref()
	if v.Kind() != KindObject || v.Object() == nil {
		return false
	}
	_, ok := v.Object().Internal.(*Generator)
	return ok
}

// LeaveGeneratorFrame is LeaveFrame for a generator whose body has ended:
// its frame goes, and what runs next is positioned where the chain it ran in
// was driven. A generator stepped by its own driver answers that driver's
// line, as LeaveFrame would; one entered as a `yield from` source answers the
// line that drove the outermost generator of the chain, whose frames stay
// (probed: `#0 file(21): D->__destruct()`, `#1 file(21): bar()`).
func (c *Ctx) LeaveGeneratorFrame() (restore func()) {
	n := len(c.stack)
	if n == 0 {
		return func() {}
	}
	at := c.stack[n-1]
	for i := n - 1; i > 0 && c.stack[i].Delegate; i-- {
		at = c.stack[i-1]
	}
	restore = c.LeaveFrame()
	if at.File != "" {
		c.file, c.line = at.File, at.Line
	}
	return restore
}

// delegationCycles reports whether handing `target` to `yield from` would make
// a generator delegate to one already delegating to it -- itself included.
//
// The chain has to be walked, not just the identity compared: `A` yielding from
// `B` while `B` yields from `A` is the same cycle one hop further out, and it
// recurses just as forever.
func delegationCycles(g *Generator, target Value) bool {
	held := target.Deref()
	if held.Kind() != KindObject {
		return false
	}
	t, ok := held.Object().Internal.(*Generator)
	if !ok {
		return false
	}
	for hop := 0; t != nil && hop < maxDelegateSteps; hop++ {
		if t == g {
			return true
		}
		inner, ok := t.delegate.(*generatorDelegate)
		if !ok {
			return false
		}
		t = inner.g
	}
	return false
}

// pull settles what a delegate's state means for this generator: while it still
// has an element this generator's current() reads through it, and once it is
// spent the body resumes with the delegate's return value.
func (g *Generator) pull(ctx *Ctx) error {
	if g.delegate == nil {
		return nil
	}
	if g.delegate.Valid() {
		err := g.snapDelegate(ctx)
		if err != nil && isPHPException(err) {
			// **The source's current() or key() throwing** continues into
			// this generator at the `yield from`, as its next() throwing
			// does: a try around the delegation catches it (probed;
			// bug71013).
			g.delegate = nil
			g.body.ResumeThrowing(err)
			return g.step(ctx)
		}
		return err
	}
	// **A delegate that threw has no return value to give**: another
	// generator drove it into an exception, and this one is thrown
	// ClosedGeneratorException at its `yield from` (mutli_yield_from_with_
	// exception) rather than resumed with NULL.
	if inner := delegatedGenerator(g.delegate); inner != nil && inner.threw {
		g.delegate = nil
		g.body.ResumeThrowing(g.closedDelegateErr(ctx))
		return g.step(ctx)
	}
	ret := g.delegate.Return()
	g.delegate = nil
	g.body.Resume(ret)
	return g.step(ctx)
}

// step enters the body until it yields something this generator can present, or
// finishes. A `yield from` over an already-spent source produces nothing, so one
// advance can take several entries.
func (g *Generator) step(ctx *Ctx) error {
	asDelegate := ctx.takeStepAsDelegate()
	if g.running {
		return ErrGeneratorRunning
	}
	if g.stuck || g.parked {
		return UnsupportedFor(ErrSuspendAcrossDriver,
			"a fiber suspension crossed a generator driver, and it cannot be advanced again")
	}
	if g.finished {
		return nil
	}
	// **The generator's own frame is on the stack while its body runs.** An
	// exception thrown inside it traces as `#0 file(N): gen()`, where N is the
	// line that *drove* the generator -- the `foreach`, the `->next()` -- not a
	// line inside the body. So the frame is pushed here, around the entry,
	// carrying the driver's position; the body then moves the location itself
	// as it executes.
	//
	// Without it a throw from a generator body traced as `#0 {main}`, one frame
	// short, for every driver.
	if !g.hidden {
		file, line := ctx.Location()
		ctx.PushCall(StackFrame{Function: g.Function, File: file, Line: line, Args: g.Args, Delegate: asDelegate})
		defer ctx.PopCall()
	}
	for taken := 0; taken < maxDelegateSteps; taken++ {
		// **The body resumes where it stopped**, and its position is its own
		// line again: a compiled body sets the location only where the line
		// changes, so without this an error raised on the `yield` line after
		// a resume was placed at the driver's line (yield_from_already_running).
		if cs, ok := g.body.(*callStack); ok && cs.atFile != "" {
			ctx.SetLocation(cs.atFile, cs.atLine)
		}
		g.running = true
		out, err := g.body.Step(ctx)
		g.running = false
		if err != nil {
			// A body that threw is done. The exception is the caller's, and
			// getReturn() stays refused: it never returned.
			g.abort()
			g.threw = true
			return err
		}
		if g.closing && (out.Kind == Yielded || out.Kind == Delegated) {
			// **A force-closed generator cannot suspend.** The engine throws
			// at the `yield` inside the finally the close is running, where a
			// try there may catch it and the unwind then carries on
			// (yield_in_force_closed_finally_error). A `yield from` is
			// refused before it touches its source: the generator it was
			// handed is never started (yield_from_force_closed).
			msg := "Cannot yield from finally in a force-closed generator"
			if out.Kind == Delegated {
				msg = `Cannot use "yield from" in a force-closed generator`
			}
			g.body.ResumeThrowing(RaiseAt(ctx, "Error", "%s", msg))
			continue
		}
		switch out.Kind {
		case Yielded:
			g.key, g.current = g.nextKey(out.Key), out.Value
			g.moves++
			g.leaf = nil
			return nil
		case Delegated:
			g.fromFile, g.fromLine = ctx.Location()
			if delegationCycles(g, out.Value) {
				// Without this the delegate resumes the generator delegating to
				// it and the two drive each other until the Go stack is gone:
				// `($gen = gen())->send($gen)` on a body of `yield from yield`
				// was a stack overflow, not an answer. The engine calls it
				// `Impossible to yield from the Generator being currently run`,
				// and throws it **inside** the body at the `yield from`: a try
				// there catches it, and uncaught it traces from the
				// generator's own frame (yield_from_already_running).
				g.body.ResumeThrowing(raiseInBody(ctx, ErrYieldFromSelf))
				continue
			}
			ctx.stepAsDelegate = isGeneratorValue(out.Value)
			d, err := NewDelegate(ctx, out.Value)
			ctx.stepAsDelegate = false
			if errors.Is(err, ErrNotDelegatable) {
				// `yield from 1` is the program's mistake, thrown into the
				// body at the `yield from` like the one above.
				g.body.ResumeThrowing(raiseInBody(ctx, err))
				continue
			}
			if err != nil {
				// **The source parked on its first run.** Priming the
				// generator handed to `yield from` reached a
				// `Fiber::suspend()` before its first yield: this generator
				// is parked behind it, delegating, exactly as if the
				// suspension had come on a later advance -- a `GeneratorDrive`
				// above carries it to the fiber, and resuming continues the
				// source (gh9916-003). Aborting here threw the chain away.
				if errors.Is(err, ErrSuspendAcrossDriver) {
					src := parkedSource(out.Value)
					if gd, ok := d.(*generatorDelegate); ok && src == nil && gd.g.parked {
						// An Iterator whose rewind() or valid() parked on
						// its first run (gh9916-007): the same, through the
						// hidden generator that drives it.
						src = gd.g
					}
					if src != nil {
						g.delegate = &generatorDelegate{g: src}
						g.parked, g.suspendedWith = true, src.suspendedWith
						return err
					}
				}
				// **An exception from the source is thrown into this
				// generator** at the `yield from` -- `valid()` throwing on
				// the first look -- where a try around the delegation catches
				// it. A gap or a protocol failure still ends the generator.
				if isPHPException(err) {
					g.body.ResumeThrowing(err)
					continue
				}
				g.abort()
				return err
			}
			g.delegate = d
			if d.Valid() {
				err := g.snapDelegate(ctx)
				if err != nil && isPHPException(err) {
					// current() or key() throwing is thrown in at the
					// `yield from` too (probed; bug71013).
					g.delegate = nil
					g.body.ResumeThrowing(err)
					continue
				}
				return err
			}
			g.delegate = nil
			g.body.Resume(d.Return())
		case Returned:
			g.finish(out.Value)
			return nil
		case Suspended:
			// The body called `Fiber::suspend()`. The generator is neither
			// finished nor broken -- the engine leaves it suspended at that
			// point and continues it when the fiber is resumed -- so aborting
			// it would be wrong twice over: it would destroy state a resume
			// needs, and it would report an unimplemented capability as a
			// failure of the body.
			//
			// It is stuck rather than dead, and the stuck flag is the point:
			// without it a second next() would re-enter the continuation with a
			// stale resumed value and look like it worked.
			// **Parked, not broken.** A `GeneratorDrive` intercepts this and
			// carries the suspension out to the fiber, then resumes the body
			// where it stopped. Reaching any other caller leaves the generator
			// parked and reports the gap, which is what it still is there.
			g.parked, g.suspendedWith = true, out.Value
			// Reported as a **gap**, not as a bare sentinel: it is one, and a
			// harness grading the run puts a sentinel in `failed` and a gap in
			// `unsupported`. The condition rides along, so `errors.Is` still
			// matches it.
			return UnsupportedFor(ErrSuspendAcrossDriver,
				"a fiber suspension crossed a generator driver")
		default:
			g.abort()
			return fmt.Errorf("%w: a generator body gave %s", ErrProtocol, out.Kind)
		}
	}
	g.abort()
	return fmt.Errorf("%w: a yield from chain did not settle", ErrProtocol)
}

// parkedSource is the generator a `yield from` was handed directly, when
// priming it parked it on a fiber suspension; nil otherwise. One behind an
// IteratorAggregate is not followed, and parking there stays the gap.
func parkedSource(v Value) *Generator {
	held := v.Deref()
	if held.Kind() != KindObject {
		return nil
	}
	g, ok := held.Object().Internal.(*Generator)
	if !ok {
		return nil
	}
	if !g.parked {
		return nil
	}
	return g
}

// OwnDelegate marks the generator a `yield from` is about to delegate to as
// held by nothing else: the compiler proved it is the fresh result of a call to
// a generator function, used only by the `yield from`.
func OwnDelegate(v Value) Value {
	if o := v.Object(); o != nil {
		if g, ok := o.Internal.(*Generator); ok {
			g.soleDelegate = true
		}
	}
	return v
}

// raiseInBody turns a refusal the driver detects at a `yield from` into the
// Error the engine throws there: positioned at the body's line and traced from
// the generator's frame, which step has pushed.
func raiseInBody(ctx *Ctx, err error) error {
	class, msg, _ := ThrowableFor(err)
	return RaiseAt(ctx, class, "%s", msg)
}

func (g *Generator) finish(ret Value) {
	g.abort()
	g.returned = true
	g.ret = ret
}

// isPHPException reports an error a PHP program could catch, as against a gap,
// a protocol failure or the close unwind.
func isPHPException(err error) bool {
	if err == nil || IsUnsupported(err) || errors.Is(err, ErrGeneratorClose) || errors.Is(err, ErrProtocol) {
		return false
	}
	if _, ok := AsThrown(err); ok {
		return true
	}
	var raised *Error
	return errors.As(err, &raised)
}

// abort ends the generator without an answer for getReturn().
func (g *Generator) abort() {
	g.finished = true
	g.delegate = nil
	g.ret = Null()
	g.key, g.current = Null(), Null()
}

// nextKey applies the automatic key rule. It is the array one: an explicit
// integer key at or above the counter moves the counter past it, so
// `yield 5 => "a"; yield "b";` gives keys 5 and 6.
func (g *Generator) nextKey(explicit Value) Value {
	if explicit.IsUndef() {
		k := g.autoKey
		g.autoKey++
		return Int(k)
	}
	if explicit.Kind() == KindInt {
		if n := explicit.RawInt(); n >= g.autoKey {
			g.autoKey = n + 1
		}
	}
	return explicit
}

// Delegate is a source a `yield from` pulls through. PHP allows three: an array,
// another Generator, and a Traversable -- and all three are covered now that
// traversable.go declares the hop into user code that the last one needs.
//
// Key and Current take a ctx and can fail because a Traversable's are PHP
// methods. Valid cannot, because its answer is established by NewDelegate and
// Advance and cached: the engine calls `valid()` exactly once per iteration,
// always straight after `rewind()` or `next()`.
type Delegate interface {
	Valid() bool
	Key(ctx *Ctx) (Value, error)
	Current(ctx *Ctx) (Value, error)
	// Advance moves past the current element, sending v where the source can
	// take one: a generator's `send()` receives it, an array discards it.
	Advance(ctx *Ctx, v Value) error
	// Return is what the `yield from` expression evaluates to once the source is
	// spent -- a generator's return value, and NULL for an array.
	Return() Value
}

// NewDelegate wraps what `yield from` was handed, and primes it. Priming here
// is what lets the accessors below be pure: a delegate is asked for its current
// element from inside another generator's advance, where there is no sensible
// place to enter a second body.
func NewDelegate(ctx *Ctx, source Value) (Delegate, error) {
	switch source.Kind() {
	case KindArray:
		return newArrayDelegate(source.Array()), nil
	case KindObject:
		if g, ok := source.Object().Internal.(*Generator); ok {
			// One already aborted by an exception cannot be delegated to at
			// all (yield_from_aborted_generator_with_children).
			if g.finished && g.threw {
				return nil, Raise("Error", "%s",
					"Generator passed to yield from was aborted without proper return and is unable to continue")
			}
			if err := g.prime(ctx); err != nil {
				return nil, err
			}
			return &generatorDelegate{g: g}, nil
		}
		// A Traversable delegates through the same hop `foreach` uses, and with
		// the same opening: the engine rewinds a `yield from new It` exactly as
		// it rewinds a foreach over one.
		trav, ok, err := ctx.traverse(source)
		if err != nil {
			return nil, err
		}
		if ok {
			// **An IteratorAggregate handing back a generator is generator
			// delegation**: the engine drives the generator itself, so its
			// frame names the `yield from` line and no `Generator->next()`
			// frame appears (gh15275-003). Driven through the iterator
			// protocol it traced as a method call from an internal frame.
			if src, isGen := trav.(GeneratorSource); isGen {
				if g := src.SourceGenerator(); g != nil && !g.started {
					if err := g.prime(ctx); err != nil {
						return nil, err
					}
					return &generatorDelegate{g: g}, nil
				}
			}
			if ft, framed := trav.(FramedTraversable); framed && iteratorSuspends(ctx, ft) {
				return newIteratorDelegate(ctx, ft)
			}
			d := &traversableDelegate{trav: trav}
			if err := trav.Rewind(ctx); err != nil {
				return nil, err
			}
			if d.valid, err = trav.Valid(ctx); err != nil {
				return nil, err
			}
			return d, nil
		}
	}
	if source.Kind() == KindObject {
		if !ctx.hasTraversableResolver() {
			// An object we cannot reach is a gap: the hop into user code is not
			// installed.
			return nil, fmt.Errorf("%w: %s", ErrDelegateUnsupported, source.Kind())
		}
		// The hop answered, and its answer was no. That is the **program**
		// being wrong -- `yield from new stdClass` -- and PHP throws for it,
		// exactly as it does for a scalar. Treating it as a gap printed
		// nothing where the engine prints the Error.
		return nil, ErrNotDelegatable
	}
	// Anything else is the *program* being wrong. Unlike `foreach`, which warns
	// and runs zero times, `yield from` throws -- checked:
	//
	//	Error: Can use "yield from" only with arrays and Traversables
	//
	// So the two look alike and are not, and neither is a gap.
	return nil, ErrNotDelegatable
}

// arrayDelegate iterates a snapshot of the array's keys. `yield from` preserves
// the source's keys rather than renumbering, which is why a generator can yield
// the same key twice.
// arrayDelegate is `yield from [...]`, and it holds a **snapshot**.
//
// It used to snapshot the keys and read each value through the array, which is
// wrong and was only unobservable because nothing shared an `*Array` across the
// delegation boundary. Settled against the engine:
//
//	$b = [1,2,3];  function h(&$r) { yield from $r; }
//	$it = h($b);  $it->current();  // 1
//	$b[1] = 77;   $it->next();  $it->current();  // 2, not 77
//
// Even by reference the delegation walks the array as it was. Same rule as
// `foreach`, and the same snapshot; see Iter in iter.go.
type arrayDelegate struct {
	keys   []Key
	values []Value
	at     int
}

func newArrayDelegate(arr *Array) *arrayDelegate {
	keys, values := arraySnapshot(arr)
	return &arrayDelegate{keys: keys, values: values}
}

func (d *arrayDelegate) Valid() bool { return d.at < len(d.keys) }

func (d *arrayDelegate) Key(_ *Ctx) (Value, error) {
	if !d.Valid() {
		return Null(), nil
	}
	return d.keys[d.at].Value(), nil
}

func (d *arrayDelegate) Current(_ *Ctx) (Value, error) {
	if !d.Valid() {
		return Null(), nil
	}
	return d.values[d.at], nil
}

// Advance discards the sent value: an array has nothing to receive it, which is
// what makes `send()` through `yield from [1,2]` look like it was dropped.
func (d *arrayDelegate) Advance(_ *Ctx, _ Value) error {
	d.at++
	return nil
}

func (d *arrayDelegate) Return() Value { return Null() }

// generatorDelegate forwards to another generator, whose own delegate chain it
// reads through in turn.
type generatorDelegate struct{ g *Generator }

func (d *generatorDelegate) Valid() bool { return !d.g.finished }

func (d *generatorDelegate) Key(ctx *Ctx) (Value, error) { return d.g.peekKey(ctx) }

func (d *generatorDelegate) Current(ctx *Ctx) (Value, error) { return d.g.peekCurrent(ctx) }

func (d *generatorDelegate) Advance(ctx *Ctx, v Value) error {
	_, err := d.g.Send(ctx, v)
	return err
}

func (d *generatorDelegate) Return() Value {
	v, _ := d.g.returnValue()
	return v
}

// ---------------------------------------------------------------------------
// Fiber
// ---------------------------------------------------------------------------

// FiberStatus is the one state PHP exposes through four predicates. Every
// transition below was settled by running the cases against the engine.
type FiberStatus uint8

const (
	// FiberNotStarted is `new Fiber($fn)` before `start()`: all four
	// predicates answer false.
	FiberNotStarted FiberStatus = iota
	// FiberSuspended is parked at a `Fiber::suspend()`, which is also the only
	// state `resume()` and `throw()` accept.
	FiberSuspended
	// FiberRunning is inside the body. A driver only sees it from within a
	// reentrant call, which is what makes it worth having rather than a bool.
	FiberRunning
	// FiberTerminated is returned or threw. Which of the two is a separate
	// question, because getReturn() words them differently.
	FiberTerminated
)

func (s FiberStatus) String() string {
	switch s {
	case FiberNotStarted:
		return "not started"
	case FiberSuspended:
		return "suspended"
	case FiberRunning:
		return "running"
	case FiberTerminated:
		return "terminated"
	}
	return "unknown"
}

// Fiber is PHP's Fiber over a resumable function.
//
// Where a generator is created by the *call* -- an eager prologue runs, and
// `ret.generator` splits it from the body -- a fiber is not. `new Fiber($fn)`
// stores a callable and runs nothing, and the arguments arrive later at
// `start()`. That falls out of the IR rather than having to be arranged: a
// fiber body has no `ret.generator`, so its `recv` opcodes sit in state 0 and
// run on the first Step, which is exactly what `start(...$args)` is.
//
// The scope is the one the package comment states: the suspensions this drives
// are the ones in the body it steps. A suspension from deeper never arrives
// here, because the emitter will not emit the call that would carry it.
type Fiber struct {
	body   Resumable
	status FiberStatus
	// returned and threw split FiberTerminated. PHP has three different
	// refusals for getReturn() and needs both flags to tell them apart; a
	// generator has one refusal and needs only `returned`.
	returned bool
	threw    bool
	ret      Value
	// suspendFile and suspendLine are where the body last suspended, where
	// a force-close's refusal of a further suspend is raised.
	suspendFile string
	suspendLine uint32
}

// NewFiber is `new Fiber($fn)`. It runs nothing: a fiber does not start until
// it is started.
func NewFiber(body Resumable) *Fiber {
	// Wrapped so a call that may suspend is followed into rather than handed
	// to this driver, which has no vocabulary for one.
	return &Fiber{body: newCallStack(body, true), ret: Null()}
}

// Start is `$f->start(...$args)`. The arguments are already bound into the
// frame -- they are its own slots, filled by the `recv`s in state 0 -- so there
// is nothing to pass here. It answers the value of the first suspension, or
// NULL if the body ran to its end without suspending.
func (f *Fiber) Start(ctx *Ctx) (Value, error) {
	// Asked first, as the engine asks it before the status.
	if ctx != nil && ctx.FiberSwitchBlocked() {
		return Null(), ErrFiberSwitchBlocked
	}
	if f.status != FiberNotStarted {
		return Null(), ErrFiberAlreadyStarted
	}
	if ctx != nil {
		if n := len(ctx.fibers); n >= 1024 && n&(n-1) == 0 {
			kept := ctx.fibers[:0]
			for _, h := range ctx.fibers {
				if h.status != FiberTerminated {
					kept = append(kept, h)
				}
			}
			ctx.fibers = kept
		}
		ctx.fibers = append(ctx.fibers, f)
	}
	return f.enter(ctx)
}

// suspendedAt is where the body stopped, from inside it.
func (f *Fiber) suspendedAt(ctx *Ctx) (string, uint32) {
	if at, ok := f.body.(interface{ SuspendedAt() (string, uint32) }); ok {
		return at.SuspendedAt()
	}
	return ctx.Location()
}

// Close force-closes a suspended fiber, as destroying one does: the body
// unwinds from its suspension running its finally blocks, the way a
// generator's close does, and a `Fiber::suspend()` reached on the way is
// `FiberError: Cannot suspend in a force-closed fiber`, thrown there
// (probed). What escapes is the caller's.
func (f *Fiber) Close(ctx *Ctx) error {
	if f == nil || f.status != FiberSuspended {
		return nil
	}
	// **Through a generator it drives, too.** The close goes into the
	// generator body stopped on the suspension and comes out of it into
	// each generator delegating to it, finally blocks running on the way
	// (gh9916-003). A `yield` reached in one of them is an ordinary yield:
	// the driver call in the fiber returns and the fiber's code carries on,
	// as the engine's does (probed).
	f.body.ResumeThrowing(ErrGeneratorClose)
	// **The unwind runs in the fiber being destroyed**, so a `Fiber::suspend()`
	// in a finally is not outside a fiber: it gets as far as the force-closed
	// refusal below, as the engine checks no fiber, then destroyed.
	if ctx != nil {
		ctx.closingFibers++
		defer func() { ctx.closingFibers-- }()
	}
	for {
		f.status = FiberRunning
		out, err := f.body.Step(ctx)
		if err != nil {
			f.status, f.threw, f.ret = FiberTerminated, true, Null()
			if isGeneratorClose(err) {
				return nil
			}
			return err
		}
		if out.Kind == Suspended {
			f.suspendFile, f.suspendLine = f.suspendedAt(ctx)
			f.body.ResumeThrowing(&Error{Class: "FiberError", Message: "Cannot suspend in a force-closed fiber",
				File: f.suspendFile, Line: f.suspendLine, Callee: "Fiber::suspend"})
			continue
		}
		f.status = FiberTerminated
		return nil
	}
}

// Resume is `$f->resume($v)`: hand a value back to the `Fiber::suspend()` that
// is parked, and answer the *next* suspension's value.
func (f *Fiber) Resume(ctx *Ctx, v Value) (Value, error) {
	if ctx != nil && ctx.FiberSwitchBlocked() {
		return Null(), ErrFiberSwitchBlocked
	}
	if f.status != FiberSuspended {
		return Null(), ErrFiberNotSuspended
	}
	f.body.Resume(v)
	return f.enter(ctx)
}

// Throw is `$f->throw($e)`: resume by raising at the suspension point instead
// of handing a value back, so a `try` around the `Fiber::suspend()` inside the
// body can catch it.
func (f *Fiber) Throw(ctx *Ctx, thrown error) (Value, error) {
	if ctx != nil && ctx.FiberSwitchBlocked() {
		return Null(), ErrFiberSwitchBlocked
	}
	if f.status != FiberSuspended {
		return Null(), ErrFiberNotSuspended
	}
	f.body.ResumeThrowing(thrown)
	return f.enter(ctx)
}

// GetReturn is `$f->getReturn()`. The order of the refusals is the engine's:
// not started is asked before returned, so a fiber that was never started says
// so rather than saying it has not returned.
func (f *Fiber) GetReturn() (Value, error) {
	switch {
	case f.status == FiberNotStarted:
		return Null(), ErrFiberNotStarted
	case f.threw:
		return Null(), ErrFiberThrew
	case !f.returned:
		return Null(), ErrFiberNotReturned
	}
	return f.ret, nil
}

// Status is the state behind the four predicates.
func (f *Fiber) Status() FiberStatus { return f.status }

// IsStarted is `$f->isStarted()`, which stays true once started -- including
// after termination.
func (f *Fiber) IsStarted() bool { return f.status != FiberNotStarted }

// IsSuspended is `$f->isSuspended()`.
func (f *Fiber) IsSuspended() bool { return f.status == FiberSuspended }

// IsRunning is `$f->isRunning()`.
func (f *Fiber) IsRunning() bool { return f.status == FiberRunning }

// IsTerminated is `$f->isTerminated()`.
func (f *Fiber) IsTerminated() bool { return f.status == FiberTerminated }

// enter runs the body until it suspends or ends. Unlike a generator's step it
// never loops: a fiber has no `yield from`, so one entry gives one outcome.
func (f *Fiber) enter(ctx *Ctx) (Value, error) {
	f.status = FiberRunning
	out, err := f.body.Step(ctx)
	if err != nil {
		f.status, f.threw, f.ret = FiberTerminated, true, Null()
		return Null(), err
	}
	switch out.Kind {
	case Suspended:
		f.status = FiberSuspended
		f.suspendFile, f.suspendLine = f.suspendedAt(ctx)
		return out.Value, nil
	case Returned:
		f.status, f.returned, f.ret = FiberTerminated, true, out.Value
		return Null(), nil
	default:
		// Created, Yielded and Delegated all mean the body handed here is a
		// generator's, not a fiber's. `new Fiber('gen')` on a generator
		// function is legal PHP, but then the *call* makes a Generator object
		// and the fiber returns it; the frame never reaches this driver. So
		// this is the emitter disagreeing with frame.go, not a program error.
		f.status = FiberTerminated
		return Null(), fmt.Errorf("%w: a fiber body gave %s", ErrProtocol, out.Kind)
	}
}

// traversableDelegate is `yield from` over a PHP object, through the hop in
// traversable.go. It is the same machine as Iter's traversable case, and for the
// same reason: the engine drives a delegated Traversable with the identical
// rewind/valid/current/key/next sequence it uses for a foreach.
type traversableDelegate struct {
	trav  Traversable
	valid bool
}

func (d *traversableDelegate) Valid() bool { return d.valid }

func (d *traversableDelegate) Key(ctx *Ctx) (Value, error) {
	if !d.valid {
		return Null(), nil
	}
	return d.trav.Key(ctx)
}

func (d *traversableDelegate) Current(ctx *Ctx) (Value, error) {
	if !d.valid {
		return Null(), nil
	}
	return d.trav.Current(ctx)
}

// Advance discards the sent value. `send()` through a `yield from` reaches the
// generator that is actually suspended, and an Iterator is not one -- it has
// nowhere to receive a value, the same as an array.
func (d *traversableDelegate) Advance(ctx *Ctx, _ Value) error {
	if err := d.trav.Next(ctx); err != nil {
		return err
	}
	valid, err := d.trav.Valid(ctx)
	d.valid = valid
	return err
}

// Return is NULL: an Iterator has no return value, so `$x = yield from new It`
// gives NULL the way it does for an array.
func (d *traversableDelegate) Return() Value { return Null() }

// FramedTraversable is a Traversable over a PHP Iterator whose methods can be
// entered as frames. MethodFrame answers the frame of one of the five methods
// when it was compiled as a state machine -- its body may suspend a fiber --
// and false for one that runs as an ordinary call. Declared here and answered
// from above, like GeneratorSource: only the emitter knows which bodies have
// frames.
type FramedTraversable interface {
	Traversable
	MethodFrame(ctx *Ctx, name string) (Resumable, bool)
}

// iteratorMethods are the five Iterator methods, in iteratorBody's states.
var iteratorMethods = [...]string{"rewind", "valid", "current", "key", "next"}

const (
	iterRewind = iota
	iterValid
	iterCurrent
	iterKey
	iterNext
	iterYield
	iterDone
)

// iteratorSuspends reports whether any of an Iterator's methods may suspend.
func iteratorSuspends(ctx *Ctx, ft FramedTraversable) bool {
	for _, name := range iteratorMethods {
		if _, ok := ft.MethodFrame(ctx, name); ok {
			return true
		}
	}
	return false
}

// newIteratorDelegate is `yield from` over an Iterator whose methods may
// suspend a fiber: rewind() calling `Fiber::suspend()` (gh9916-007).
//
// **The Iterator is driven by a hidden generator.** A traversableDelegate
// calls the methods as ordinary calls, and a suspension inside one has
// nowhere to park. A generator does: its body is a callStack, so a method
// entered as a frame suspends the stack, the generator parks, and the
// delegating generator parks behind it exactly as it does behind a generator
// source -- a `GeneratorDrive` carries it to the fiber, resuming continues
// the method, and destroying the fiber unwinds the method's finally blocks
// before the delegator's. The body calls the methods in the order a
// traversableDelegate does -- rewind, valid, then current and key per
// element, next and valid per advance -- and yields what they answer.
//
// It answers the delegate even when priming parked, with the error: the step
// at the `yield from` parks the delegator on it.
func newIteratorDelegate(ctx *Ctx, ft FramedTraversable) (Delegate, error) {
	file, line := ctx.Location()
	body := &iteratorBody{trav: ft, file: file, line: line}
	g := &Generator{
		body: newCallStack(body, false), hidden: true, soleDelegate: true,
		ret: Null(), key: Null(), current: Null(),
	}
	d := &generatorDelegate{g: g}
	if err := g.prime(ctx); err != nil {
		if errors.Is(err, ErrSuspendAcrossDriver) && g.parked {
			return d, err
		}
		return nil, err
	}
	return d, nil
}

// iteratorBody is the hidden generator's body: the Iterator protocol as a
// resumable. A method with a frame is entered as a `Called`, so its callStack
// holds it while it runs; one without is an ordinary call through the
// Traversable. The sent value is discarded, as traversableDelegate does.
type iteratorBody struct {
	trav FramedTraversable
	// file and line are the `yield from`, which is where the engine calls the
	// methods from.
	file  string
	line  uint32
	state int
	// awaiting is a method entered as a frame whose answer is resumed.
	awaiting bool
	resumed  Value
	thrown   error
	cur      Value
}

func (b *iteratorBody) Resume(v Value)           { b.resumed = v }
func (b *iteratorBody) ResumeThrowing(err error) { b.thrown = err }

func (b *iteratorBody) Step(ctx *Ctx) (Outcome, error) {
	if b.thrown != nil {
		// A method threw, or the generator is thrown into or closed at its
		// yield: either way the iteration is over.
		err := b.thrown
		b.thrown, b.state = nil, iterDone
		return Outcome{}, err
	}
	for {
		if b.state == iterDone {
			return Outcome{Kind: Returned, Value: Null()}, nil
		}
		if b.state == iterYield {
			// Resumed after the yield: the next element.
			b.state = iterNext
			continue
		}
		var v Value
		if b.awaiting {
			v, b.awaiting = b.resumed, false
		} else {
			ctx.SetLocation(b.file, b.line)
			if f, ok := b.trav.MethodFrame(ctx, iteratorMethods[b.state]); ok {
				b.awaiting = true
				return Outcome{Kind: Called, Frame: f}, nil
			}
			var err error
			if v, err = b.call(ctx); err != nil {
				b.state = iterDone
				return Outcome{}, err
			}
		}
		switch b.state {
		case iterRewind, iterNext:
			b.state = iterValid
		case iterValid:
			if !v.Bool() {
				b.state = iterDone
				continue
			}
			b.state = iterCurrent
		case iterCurrent:
			b.cur, b.state = v, iterKey
		case iterKey:
			b.state = iterYield
			return Outcome{Kind: Yielded, Key: v, Value: b.cur}, nil
		}
	}
}

// call performs the current state's method as an ordinary call.
func (b *iteratorBody) call(ctx *Ctx) (Value, error) {
	switch b.state {
	case iterRewind:
		return Null(), b.trav.Rewind(ctx)
	case iterValid:
		ok, err := b.trav.Valid(ctx)
		return Bool(ok), err
	case iterCurrent:
		return b.trav.Current(ctx)
	case iterKey:
		return b.trav.Key(ctx)
	default:
		return Null(), b.trav.Next(ctx)
	}
}

// callStack is a resumable body together with the frames it has called into.
//
// A `Fiber::suspend()` suspends the whole stack it runs on, so a function that
// calls a suspending one has to be able to give control up as well. The CPS
// rewrite gives it the only shape that works without a Go stack to park:
// the caller **builds** the callee's frame and hands it up as `Called` rather
// than performing the call. Somebody then has to hold those frames, and this is
// that somebody.
//
// It is itself a `Resumable`, so a driver wraps its body in one and is otherwise
// unchanged: `Called` never escapes, and what a driver sees is whatever the
// innermost frame produced. That is the point -- the generator and fiber drivers
// disagree about what a suspension *means*, and neither of them needs to know
// that a call is in progress underneath.
//
// The stack is explicit on purpose. It is the thing a goroutine would have made
// implicit, and the thing a byte-exact backtrace and a byte-exact destructor
// order have to be read off.
type callStack struct {
	root Resumable
	// rootFrame is whether this stack pushes a frame for the body itself.
	//
	// A generator's driver already does: it knows the function's name and the
	// arguments it was called with, and the frame is positioned at the line
	// that *drove* it. A fiber's does not -- it holds a callable, entered from
	// inside `start()` or `resume()` -- so the body's frame is pushed here,
	// located at `[internal function]` because that is where the engine enters
	// it from.
	rootFrame bool
	inner     []calledFrame
	// atFile and atLine are where the last step stopped, read before its
	// frames were popped: where a suspended fiber is suspended.
	atFile string
	atLine uint32
}

// SuspendedAt is where the body last stopped.
func (s *callStack) SuspendedAt() (string, uint32) { return s.atFile, s.atLine }

// calledFrame is one frame entered by a suspending call, with the position of
// the call that entered it.
//
// The position is captured **when the call happens**, not when the frame runs:
// by the time a resume re-enters it, the caller's own position has moved on,
// and a backtrace names where a function was called from rather than where it
// currently is.
type calledFrame struct {
	body Resumable
	file string
	line uint32
}

// InternalFrame is a TracedFrame that is an engine function's: what it enters
// is located at `[internal function]`, as MarkInternalCall makes it.
type InternalFrame interface{ TraceInternal() bool }

// TracedFrame is a resumable that can name itself in a backtrace. A generated
// frame implements it; anything that does not simply contributes no entry,
// which is what a frame with no PHP name should do.
type TracedFrame interface {
	TraceName() string
	TraceArgs() []Value
}

// newCallStack wraps a body so calls that suspend can be followed into.
func newCallStack(root Resumable, rootFrame bool) *callStack {
	return &callStack{root: root, rootFrame: rootFrame}
}

// top is the frame actually running: the innermost call, or the body itself.
func (s *callStack) top() Resumable {
	if n := len(s.inner); n > 0 {
		return s.inner[n-1].body
	}
	return s.root
}

// Resume hands a value to the **innermost** frame, which is the one that
// suspended. Handing it to the root would resume a frame that is waiting on a
// call rather than on the driver.
func (s *callStack) Resume(v Value) { s.top().Resume(v) }

// ResumeThrowing raises at the innermost frame's suspension point, so the
// `try` around it in that function is the one that catches -- not a `try` in
// whatever called it.
func (s *callStack) ResumeThrowing(err error) { s.top().ResumeThrowing(err) }

// Step runs the innermost frame and follows calls, so the driver only ever sees
// an outcome that is genuinely about the body: a yield, a suspension, or the
// body's own return.
//
// A callee's `Returned` is **not** the body returning. It is this stack popping
// one frame and resuming its caller with the value, which is what makes a
// suspending call read as an ordinary call from inside the caller.
func (s *callStack) Step(ctx *Ctx) (Outcome, error) {
	for {
		// **The frames a suspending call entered are on the stack while they
		// run.** They are pushed per step rather than once, because a CPS frame
		// is entered many times: the Go stack that held them is gone by the
		// time control comes back, which is exactly what makes them explicit
		// here instead.
		pop := s.pushFrames(ctx)
		out, err := s.top().Step(ctx)
		// **Read the position before popping.** `PopCall` restores the
		// position from the frame it removes, so after the undo the location is
		// the driver's -- `[internal function]` for the body's own frame -- and
		// a call captured then names nowhere. It has to be the line inside the
		// caller that made the call.
		file, line := ctx.Location()
		s.atFile, s.atLine = file, line
		pop()
		if err != nil {
			// **An exception unwinds frame by frame**, as PHP's does: each
			// caller gets it raised at its own suspension point, so its `try`
			// can catch it and its `finally` can run. Abandoning the whole
			// stack instead let an exception from a suspending callee sail
			// straight past a `try` that was written around the call.
			if n := len(s.inner); n > 0 {
				s.inner = s.inner[:n-1]
				s.top().ResumeThrowing(err)
				continue
			}
			return Outcome{}, err
		}
		switch out.Kind {
		case Called:
			if out.Frame == nil {
				return Outcome{}, fmt.Errorf("%w: a call outcome carried no frame", ErrProtocol)
			}
			s.inner = append(s.inner, calledFrame{body: out.Frame, file: file, line: line})
		case Returned:
			if n := len(s.inner); n > 0 {
				s.inner = s.inner[:n-1]
				s.top().Resume(out.Value)
				continue
			}
			return out, nil
		default:
			return out, nil
		}
	}
}

// pushFrames puts the called frames on the call stack, outermost first, and
// answers the undo.
//
// The trace's shape is the engine's: a function reached through a suspending
// call names the line it was called from, in the function that called it.
func (s *callStack) pushFrames(ctx *Ctx) func() {
	pushed := 0
	if s.rootFrame {
		if named, ok := s.root.(TracedFrame); ok {
			ctx.PushCall(StackFrame{
				Function: named.TraceName(), File: InternalFrameFile, Args: named.TraceArgs(),
			})
			pushed++
		}
	}
	for _, f := range s.inner {
		named, ok := f.body.(TracedFrame)
		if !ok || named.TraceName() == "" {
			continue
		}
		ctx.PushCall(StackFrame{
			Function: named.TraceName(), File: f.file, Line: f.line, Args: named.TraceArgs(),
		})
		if in, ok := f.body.(InternalFrame); ok && in.TraceInternal() {
			ctx.MarkInternalCall(named.TraceName())
		}
		pushed++
	}
	return func() {
		for i := 0; i < pushed; i++ {
			ctx.PopCall()
		}
	}
}

// RunUndriven enters a body that may suspend from a place with no driver: an
// ordinary call, rather than a fiber resuming it or a caller that was itself
// rewritten into a state machine.
//
// **Suspending there is a PHP error, not a gap.** `Fiber::suspend()` outside a
// fiber is `FiberError: Cannot suspend outside of a fiber`, and a script whose
// top level suspends says exactly that -- so the honest answer is the
// diagnostic, and refusing to compile the body reported a missing capability
// where PHP has a message.
//
// The exception is reaching here *inside* a fiber, which means the call arrived
// by a path the rewrite could not see -- a dynamic call, say. Then the
// suspension is real and unimplementable from this frame, and the refusal is
// the honest answer after all.
func RunUndriven(ctx *Ctx, body Resumable, name string) (Value, error) {
	stack := newCallStack(body, false)
	for {
		out, err := stack.Step(ctx)
		if err != nil {
			return Value{}, err
		}
		switch out.Kind {
		case Returned:
			return out.Value, nil
		case Suspended:
			// Suspending with no fiber is caught **at the suspension point**
			// by `SuspendOutsideFiber`, so that the body's own `try` can catch
			// it -- reaching here means a fiber is running and the call arrived
			// by a path the rewrite could not see.
			return Value{}, Unsupported("%s", "call to "+name+", which suspends into its caller")
		default:
			return Value{}, fmt.Errorf("%w: an undriven body gave %s", ErrProtocol, out.Kind)
		}
	}
}

// SuspendOutsideFiber is the check a suspension site makes before giving
// control up: with no fiber running there is nobody to give it to, and PHP says
// `FiberError: Cannot suspend outside of a fiber`.
//
// **Raised at the suspension point, not in the driver.** The engine raises it
// where `Fiber::suspend()` was written, so a `try` around that call catches it;
// raising it after the body had already handed `Suspended` out put the error
// outside every handler the body has.
//
// The frame names the call, as PHP does: `#0 file(3): Fiber::suspend(1)`.
func SuspendOutsideFiber(ctx *Ctx, v Value) error {
	return suspendCheck(ctx, []Value{v})
}

// SuspendOutsideFiberBare is the check for `Fiber::suspend()` written with
// no argument, whose frame shows none.
func SuspendOutsideFiberBare(ctx *Ctx) error { return suspendCheck(ctx, nil) }

func suspendCheck(ctx *Ctx, args []Value) error {
	if ctx.CurrentFiber() != nil || ctx.closingFibers > 0 {
		// Inside a fiber a tick function still may not switch: the engine
		// throws there, after the "outside a fiber" check (zend_fibers.c).
		if !ctx.FiberSwitchBlocked() {
			return nil
		}
		file, line := ctx.Location()
		ctx.PushCall(StackFrame{
			Function: "Fiber::suspend", File: file, Line: line, Args: args,
		})
		err := AttachStack(ctx, Raise("FiberError", "%s", "Cannot switch fibers in current execution context"))
		ctx.PopCall()
		return err
	}
	file, line := ctx.Location()
	ctx.PushCall(StackFrame{
		Function: "Fiber::suspend", File: file, Line: line, Args: args,
	})
	// Attached while the frame is live; the caller attaches too, but by then
	// this one is popped.
	err := AttachStack(ctx, Raise("FiberError", "%s", "Cannot suspend outside of a fiber"))
	ctx.PopCall()
	return err
}

// GeneratorDrive performs one driver call on a generator as a resumable, so a
// `Fiber::suspend()` inside the generator's body reaches the fiber instead of
// being refused.
//
// **This is what a generator driver could not do on its own.** `$g->next()` is
// an ordinary call, and a suspension inside the body has to travel out through
// it -- which is only possible if the frame that made the call is itself a
// state machine. It is, when the call was rewritten into a suspension point, and
// then the driver call is this.
//
// The generator is left **parked** rather than broken while the fiber is away:
// its body is stopped mid-step, and resuming the fiber resumes it exactly there.
type GeneratorDrive struct {
	g *Generator
	// advance performs the driver call. It runs once; after a suspension the
	// generator continues from where its body stopped instead of starting the
	// call again, which would hand the body a second resumed value.
	advance func(*Ctx) error
	// result is what the driver call answers once it completes -- `current()`
	// has one, `next()` does not -- read after the advance rather than before,
	// since advancing is what produces it.
	result func() Value
	begun  bool
	// parkedOnce is a call that went out to the fiber and came back: what
	// its advance read before the suspension is not its answer.
	parkedOnce bool
	// reread answers it again once the body has moved on, for the calls
	// whose answer is the element (current, key, valid, send). Nil for the
	// ones that answer nothing.
	reread func(*Ctx) (Value, error)
	// name and args are the driver call's own frame, `Generator->current()`,
	// which a backtrace shows under the body it enters (Trace).
	name string
	args []Value
}

// Trace names the driver call, so the callStack holding it pushes its frame:
// `#1 file(N): Generator->current()`, with the body entered from inside it at
// `[internal function]`. Without it the trace through a split driver call was
// one frame short, and after a resume the body named the resuming line.
func (d *GeneratorDrive) Trace(name string, args []Value) *GeneratorDrive {
	d.name, d.args = name, args
	return d
}

func (d *GeneratorDrive) TraceName() string  { return d.name }
func (d *GeneratorDrive) TraceArgs() []Value { return d.args }

// TraceInternal marks the frame an engine function's (InternalFrame).
func (d *GeneratorDrive) TraceInternal() bool { return true }

// Reread gives a drive the call that answers it afresh after a suspension:
// `current()` on a generator that parked before its first yield read NULL
// on the way out, and the element exists only once the fiber resumed it.
func (d *GeneratorDrive) Reread(f func(*Ctx) (Value, error)) *GeneratorDrive {
	d.reread = f
	return d
}

// DriveGenerator builds one. `advance` performs the call and `result` reads its
// answer afterwards; `result` may be nil for a call that answers nothing.
func DriveGenerator(g *Generator, advance func(*Ctx) error, result func() Value) *GeneratorDrive {
	return &GeneratorDrive{g: g, advance: advance, result: result}
}

// Resume hands the fiber's value to the parked body.
func (d *GeneratorDrive) Resume(v Value) { d.g.resumeParked(v, nil) }

// ResumeThrowing raises at the parked body's suspension point, so the `try`
// around the `Fiber::suspend()` inside the generator is the one that catches.
func (d *GeneratorDrive) ResumeThrowing(err error) { d.g.resumeParked(Null(), err) }

func (d *GeneratorDrive) Step(ctx *Ctx) (Outcome, error) {
	var err error
	// A generator **already** parked when this driver call begins is held by
	// another fiber's suspension, not by this call: the call is refused as a
	// resume of a running generator, and the park it finds is not this
	// frame's to carry out.
	held := !d.begun && d.g.parked
	if !d.begun {
		d.begun = true
		err = d.advance(ctx)
	} else {
		// The body was parked inside the advance loop, so the loop is what
		// continues -- not the driver call, which has already had its effect on
		// the generator's state.
		err = d.g.continueParked(ctx)
	}
	if d.g.parked && !held {
		d.parkedOnce = true
		for cur, hop := d.g, 0; cur != nil && hop < maxDelegateSteps; hop++ {
			if cur.parked {
				cur.carried = true
			}
			cur = delegatedGenerator(cur.delegate)
		}
		return Outcome{Kind: Suspended, Value: d.g.suspendedWith}, nil
	}
	if err != nil {
		return Outcome{}, err
	}
	if d.parkedOnce && d.reread != nil {
		v, err := d.reread(ctx)
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{Kind: Returned, Value: v}, nil
	}
	v := Null()
	if d.result != nil {
		v = d.result()
	}
	return Outcome{Kind: Returned, Value: v}, nil
}

// CallOnce is a resumable that performs an ordinary call and returns.
//
// It exists so that a call site the rewrite turned into a suspension point is
// safe even when the callee turns out not to suspend after all. The decision to
// split is made statically and conservatively -- `$x->next()` may be a Generator
// driver and may be a method on anything else -- and the run-time answer is
// this: enter it, get the value, carry on. Nothing suspends, so the caller
// resumes immediately.
func CallOnce(run func(*Ctx) (Value, error)) Resumable { return &callOnce{run: run} }

type callOnce struct {
	run    func(*Ctx) (Value, error)
	thrown error
}

func (c *callOnce) Resume(Value)             {}
func (c *callOnce) ResumeThrowing(err error) { c.thrown = err }

func (c *callOnce) Step(ctx *Ctx) (Outcome, error) {
	if c.thrown != nil {
		return Outcome{}, c.thrown
	}
	v, err := c.run(ctx)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Kind: Returned, Value: v}, nil
}

// GeneratorSource is a Traversable that drives a Generator.
//
// `yield from new It()` over an `IteratorAggregate` resolves to a Traversable
// wrapping whatever `getIterator()` returned, which is very often a Generator --
// and a `Fiber::suspend()` inside *that* body parks it. Finding it needs the
// wrapper to say what it is driving, and the wrapper is built above this
// package: declared here, answered from above, like `ObjectProperties` and
// `CallValue`.
type GeneratorSource interface{ SourceGenerator() *Generator }

// delegatedGenerator is the generator one `yield from` hands off to, whichever
// shape the delegate took. Nil when the delegate is not driving a generator at
// all -- an array, or a Traversable that is something else.
func delegatedGenerator(d Delegate) *Generator {
	switch inner := d.(type) {
	case *generatorDelegate:
		return inner.g
	case *traversableDelegate:
		if src, ok := inner.trav.(GeneratorSource); ok {
			return src.SourceGenerator()
		}
	}
	return nil
}

// parkedInChain is the generator actually stopped on a fiber suspension,
// following `yield from` inwards. A delegating generator carries no suspension
// of its own -- its body contains no `Fiber::suspend()` -- so the one that
// stopped can be any depth down the chain.
// **The innermost one**, not the first. Every generator in the chain is marked
// parked -- each of them is stopped -- so returning the first found the
// *outermost*, whose body is sitting at its `yield from` rather than at the
// suspension. Resuming that one handed the fiber's value to the wrong body, and
// the chain then answered the element it was parked on for ever.
func (g *Generator) parkedInChain() *Generator {
	var innermost *Generator
	for cur, hop := g, 0; cur != nil && hop < maxDelegateSteps; hop++ {
		if cur.parked {
			innermost = cur
		}
		cur = delegatedGenerator(cur.delegate)
	}
	return innermost
}

// heldByFiber reports that the generator, or the one at the far end of its
// `yield from` chain, is parked: stopped on a `Fiber::suspend()` that a
// suspended fiber will resume. **To the engine that generator is running** --
// it is on the fiber's stack -- so resuming it from anywhere else, `next()`,
// `send()` or `throw()`, is `Cannot resume an already running generator`, the
// check the engine makes on the chain's leaf. Its state is left alone: the
// fiber resumes it where it stopped.
func (g *Generator) heldByFiber() bool {
	for cur, hop := g, 0; cur != nil && hop < maxDelegateSteps; hop++ {
		if cur.parked && cur.carried {
			return true
		}
		cur = delegatedGenerator(cur.delegate)
	}
	return false
}

// resumeParked hands the fiber's value to whichever body in the chain stopped,
// and clears the parked marks above it so the chain can advance again.
func (g *Generator) resumeParked(v Value, thrown error) {
	p := g.parkedInChain()
	if p == nil {
		return
	}
	for cur, hop := g, 0; cur != nil && hop < maxDelegateSteps; hop++ {
		cur.parked, cur.carried = false, false
		if cur == p {
			break
		}
		cur = delegatedGenerator(cur.delegate)
	}
	if thrown != nil {
		p.body.ResumeThrowing(thrown)
		return
	}
	p.body.Resume(v)
}

// continueParked advances after the chain's parked body has been resumed. The
// **delegating** generator continues by pulling from its delegate, not by
// stepping its own body -- its body is sitting at the `yield from`.
func (g *Generator) continueParked(ctx *Ctx) error { return g.continueParkedAs(ctx, false) }

// continueParkedAs is continueParked for a generator a `yield from` above it
// delegates to (asDelegate), which its frame records.
func (g *Generator) continueParkedAs(ctx *Ctx, asDelegate bool) error {
	if inner := delegatedGenerator(g.delegate); inner != nil {
		// **This generator's frame lies under the delegate's** while the
		// delegate continues, as on an ordinary resume: `#0 file(8):
		// inner()`, `#1 [internal function]: outer()`. Continuing straight
		// into the delegate left the delegator out of the trace.
		file, line := ctx.Location()
		ctx.PushCall(StackFrame{Function: g.Function, File: file, Line: line, Args: g.Args, Delegate: asDelegate})
		if g.fromFile != "" {
			ctx.SetLocation(g.fromFile, g.fromLine)
		}
		err := inner.continueParkedAs(ctx, true)
		ctx.PopCall()
		if err == nil {
			// **The delegate's cached state is stale.** A Traversable delegate
			// caches `valid` and refreshes it in `Advance`, and resuming a
			// parked body goes straight to the generator instead -- so the
			// element the inner one just produced is invisible until the cache
			// is asked again. Without this the chain reported the value it was
			// parked on, for ever.
			if td, ok := g.delegate.(*traversableDelegate); ok {
				valid, verr := td.trav.Valid(ctx)
				if verr != nil {
					g.abort()
					return verr
				}
				td.valid = valid
			}
		} else {
			if errors.Is(err, ErrSuspendAcrossDriver) {
				if p := g.parkedInChain(); p != nil {
					g.parked, g.suspendedWith = true, p.suspendedWith
				}
				return err
			}
			// **What the delegate threw continues into this body** at its
			// `yield from`, as it does on an ordinary resume -- and so does
			// the close a destroyed fiber unwinds its stack with, which is
			// what runs this generator's finally blocks after the one it
			// delegates to (gh9916-003: `Finally (inner)`, then `Finally`).
			if isPHPException(err) || isGeneratorClose(err) {
				g.delegate = nil
				g.body.ResumeThrowing(err)
				return g.step(ctx)
			}
			g.abort()
			return err
		}
		return g.pull(ctx)
	}
	ctx.stepAsDelegate = asDelegate
	return g.step(ctx)
}
