# Handoff: implement frames for resumable functions

Written 2026-08-29 by the compiler-side session for the runtime-side one. The
compiler half is done and landed; this is the runtime half, which is yours
because it lives in `runtime/php` and `runtime/interp`.

Read [IR-DESIGN.md](./IR-DESIGN.md) § "The frame of a resumable function" and
§ "Suspension is the compiler's business, not the runtime's" first. This
document is the task, not the rationale.

## What the compiler now produces

`internal/lower` lowers PHP to an IR; `lower.Resumable` rewrites any function
that suspends into a state machine. See it with:

```sh
php2lower -cps -print internal/lower/testdata/resumable.php
```

```
func counter -> zval {
  .frame [ $rows: zval, $seen: zval, $__iter5: *iter, $r: zval, $__t6: zval, $reply: zval ]
  .resumable [ bb0, bb1, bb4, bb6 ]
  .entry bb9

bb1:
  %7:zval = frame.resumed
  ...
bb3:
  frame.set_state 2
  ret.yield %9

bb9:
  %12:i64 = frame.state
  dispatch %12 -> bb0, bb1, bb4, bb6
}
```

Suspension is a **return** that records where to resume. Resuming is an
**ordinary call** that dispatches on the recorded state. Nothing underneath needs
to know suspension exists — which is the point: a runtime that implements
suspension needs a real stack per instance, and that is what this removes.

## What to build

1. **A frame type in `runtime/php`.** Heap-allocated, one per generator or fiber
   instance, holding:
   - the slots the IR declares in `.frame`
   - the state number (`frame.state` / `frame.set_state`)
   - the value handed to the last `resume()` (`frame.resumed`)
2. **The driver objects over it** — `Generator` and `Fiber` — implemented as
   *calls into the state machine*, not as coroutines:
   - `resume(frame, value)` sets the resumed value and calls the function
   - `ret.yield` / `ret.suspend` / `ret.generator` are the ways it comes back
   - `getReturn`, `send`, `throw`, and the fiber state predicates on top
3. **A representation decision** the compiler deliberately did not make: a
   generated struct per function, or a slice indexed by slot. Sizes below.

## Constraints that are not negotiable

- **Every declared slot must live as long as the frame does.** Do not free a
  slot after its last read. PHP keeps a generator's locals alive until the
  generator itself dies and orders their `__destruct` by that; dropping one early
  moves output that `php2phpt` grades byte-exact. `FrameSlot.Compiler` marks the
  slots that *are* narrowable (they are the compiler's own temporaries and the
  program cannot name them) — but nothing narrows them yet, and doing so is a
  separate change with its own measurement.
- **A slot's declared type is authoritative.** It is the join of every write, so
  a slot written `*obj` in one place and an int in another is declared `zval`.
  Do not re-derive it. In the other direction, `$__iterN: *iter` is a foreach
  iterator and must not be boxed as a `zval`.
- **The frame is per instance**, not per function.

## Sizes, measured

| corpus | frames | slots | per frame | narrowable | largest |
|---|---|---|---|---|---|
| `Zend/tests` | 418 | 587 | 1.4 | 312 (53.2%) | 18 slots |
| CakePHP `src/` | 12 | 66 | 5.5 | 23 (34.8%) | 14 slots |

Frames are small. A per-function generated struct is likely cheaper than a
slice, but that is your call and worth measuring rather than assuming.

## The boundary, stated plainly

**This does not remove the interpreter's goroutine-based fibers.**
`runtime/interp` executes op_arrays, not this IR, so it keeps whatever mechanism
it has. The frame is for compiled code. If the two end up sharing a `Generator`
surface that is a bonus, not a requirement, and the interpreter's behaviour must
not move — `php2phpt` counts are the check.

## What is not done on the compiler side

- Nothing **allocates or emits** a frame. There is no emitter, so the entire
  resumable path has been verified structurally and has never run. Yours will be
  the first execution of any of it; expect the IR to be wrong somewhere, and say
  so rather than working around it.
- Two suspensions sharing one continuation bails rather than guessing at the
  numbering. Neither corpus contains one.
- `ZEND_DISCARD_EXCEPTION` (28 `Zend/tests` bodies) — a `return` inside a
  `finally` swallowing the in-flight exception — is still unmodelled.

## Where things are

- `internal/lower/cps.go` — `Resumable`, `frameOf`, and the slot rules
- `internal/lower/ir.go` — `FrameSlot`, `Function.Frame` / `.States` / `.Entry`
- `internal/lower/lower_test.go` — `TestFrameHoldsEveryPlace`,
  `TestFrameTypeIsTheJoinOfEveryWrite`, `TestResumable*`
- `php2lower -cps <dir>` — the corpus measurements above
- `php2lower -verify <dir>` — the invariant the whole design rests on: no SSA
  value is used outside its defining block, so the frame is exactly the places
