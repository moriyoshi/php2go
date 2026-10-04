# Control Flow, Frames and Resumable Functions

## Summary

The lowerer represents control flow as a graph, not as formatted terminator
strings or spliced oplines. Exception regions participate in that graph;
`finally` bodies are cloned per exit; and a resumable function is rewritten to
a state machine whose suspension is a return and whose resumption is an
ordinary call.

These choices keep suspension and structured unwinding out of `runtime/php`.
The compiled path owns its generated frames, while the interpreter may keep a
different implementation, including goroutine-backed fibers.

## CFG and regions

`Block.Succs` is populated where terminators are formed. Predecessors,
dominators, dominance frontiers, back edges and natural loops are derived from
that structure. Exception edges count: a handler can be entered from anywhere
in its protected range, so a CFG that ignores regions permits unsound promotion
and motion.

Region membership is positional. A try/catch/finally range can begin in the
middle of a basic block in Zend's oplines, so block leaders must include every
range boundary before regions are assigned.

The invariant checked by `-verify` is that an SSA value is not used outside the
block that defines it unless it travels through an explicit block parameter or
lives in a place/frame slot.

## `finally` is copied control flow

Inlining a `finally` means cloning its blocks and remapping their internal jump
targets once for each exit. Splicing its oplines into another block loses its
branches and can execute both arms of a conditional.

Each block records the finally bodies it is inside through `Block.InFinallyOf`.
That fact is inherited by a clone and lets handler selection avoid routing an
exception back into the body already handling it. Handler and unwind passes run
to a fixed point because cloning creates new paths that earlier passes must
consider.

A return inside a finally can discard an exception already in flight.
`ZEND_DISCARD_EXCEPTION` is therefore control-flow state, not a no-op. Likewise,
an exception thrown by a finally replaces the active exception and chains the
old one as `previous` according to the engine's ordering.

`exit` is not an exceptional unwind. It terminates the process at the point of
the call, so inlined finally bodies must not run on its way out.

## Resumable frames

A resumable function is transformed into a state machine:

- suspension stores a state and returns a tagged result;
- resumption supplies a value, calls the function again and dispatches from the
  stored state;
- every place that survives a suspension lives in a heap frame owned by that
  generator or fiber instance;
- compiler temporaries can use narrow frame fields only when the frame schema
  says so; PHP-visible locals retain their joined type and lifetime.

The frame is per instance, never per function. Slots must live until the frame
does even when ordinary liveness says their last read has passed, because PHP
observes destruction and reference release order.

Bare `yield` has an unused value operand; that is a real null yield, not an
unrenderable operand. Generator return, yield and fiber suspension are separate
result tags even though they all leave the generated Go function.

## Generator and fiber drivers

The compiled generator/fiber driver repeatedly calls the generated state
machine. `send`, `throw`, `getReturn` and status predicates are protocol layered
over those calls. `yield from` keeps a delegation chain and proxies live values
rather than copying an iterable snapshot.

The interpreter is a separate consumer of `runtime/php`. Its fiber driver may
use goroutines because the interpreter still has an implicit execution stack.
The compiled CPS transform does not prove the interpreter must use the same
representation.

**The compiled side is CPS, and that is settled, not open** (2026-08-29,
re-measured 2026-09-17; see TODO). A goroutine would give the control flow for
free -- a suspension blocks whatever stack it happens on, at any depth, through
any number of generator drivers -- and that is exactly why it is the wrong
answer here: it makes the suspension *implicit*. What php2go is graded on is
byte-exact backtraces and byte-exact destructor ordering, and both need the
suspension to stay explicit data that the emitter can see and order. The current
goroutine in `runtime/interp` is a stack-parking workaround, not a concurrency
requirement.

The cost of CPS was measured rather than assumed, because "it taints the whole
program" is reasoning from the header. Over the 70 suspending corpus files (196
functions), the set that must become resumable is 46.4% by direct call edges and
53.1% with opaque calls treated soundly -- so opacity costs 6.6%, and knowing a
receiver's class is worth 0.0%. Blast radius per function: median 0, p90 0, max
31.

## Frames and observable provenance

Functions, methods, closures, eval, hooks, magic methods, destructors and clone
operations can all create observable frames. The declaring class names a method
body; the called class carries late-static binding; the call site's file/line is
where an error or trace says the call occurred.

Frame teardown releases compiled variables before an exception reaches the
caller's catch. The exception itself and the arguments stored in its trace stay
rooted while that release happens.

## Relevant files

- `internal/lower/cfg.go`, `dom.go`, `lower.go` — graph and region formation.
- `internal/lower/cps.go`, `ir.go` — state-machine rewrite and frame schema.
- `internal/gogen/gogen.go` — generated frame types, dispatch and unwind.
- `runtime/php/frame.go`, `resumable.go` — shared frame/result protocol.
- `runtime/interp/generator.go`, `fiber.go`, `throw.go` — interpreter drivers.
- `.agents/docs/FRAME-HANDOFF.md` — the historical compiler/runtime handoff.

## Test coverage

`internal/lower/testdata/{regions,finally,finallybranch,finallyline,resumable,
fiber}.php` and their IR goldens pin structure. Executed behavior belongs in
`cmd/php2gotest/testdata/{generators,yield_from,finally_nesting}.php` and the
interpreter differential suite. Request-shutdown ordering belongs in
`testdata/lifetime`.

## Pitfalls

- Do not derive CFG edges by reparsing rendered terminators.
- Do not omit exception edges from dominance or liveness.
- Do not release frame slots at last use; PHP observes lifetime, not Go
  liveness.
- Do not work around a transform defect in the runtime driver. The state
  machine is a compiler artifact and should be corrected there.
- A hang is an errored run, not a gap or a wrong-output comparison.
