# Object Lifetime

## Summary

PHP frees an object when its refcount reaches zero, and `gc_collect_cycles()`
only exists for what refcounting cannot reach. php2go has no refcounts: it
answers "is this still alive" by walking the roots, at the points where the
answer could become visible — `gc_collect_cycles()`, an `unset`, an assignment
over the last name, and a frame's locals going away. The walk lives in
`runtime/interp/gc.go`.

That substitution is faithful for *whether* an object dies. It is not
automatically faithful for *when*, and everything hard here is a `when`.

## Compiled cycle collection (2026-10-03)

Compiled output now has a bounded collector for plain object cycles at direct
calls in the actual entry body. The emitter proves no other SSA value is live
across the call, excludes resumable and exception-region bodies, and publishes
the main places. The runtime marks definite roots from globals, statics,
callbacks and the context; it separately checks compiler temporary slots and
refuses when one may hold an object outside those roots. Unreachable objects
are counted only when they lie on a cycle or hang from one, because an acyclic
unreachable object would already have died by PHP refcounting. Releasing the
cycle's handles makes a second collection return zero.

This collector refuses array references, arrays reachable from dead objects,
destructors, weak holders, opaque native state, and multiple freed handles when
handle numbers can be printed. A single freed handle has no order ambiguity.
The older one-sided observation check remains the fallback at other call
sites and when a bounded case has no observable collection effect. See
JOURNAL batch 151 and `runtime/php/gccollect.go`.

## Unreachable is not freed

They read as synonyms and are two different moments:

| Moment | What it is | What it is observable through |
|---|---|---|
| unreachable | the walk found no root reaching it | nothing directly |
| destructed | `__destruct` has run | its own side effects |
| freed | the storage is gone | weak references clearing, the handle being reused |

A destructor sits *between* the second and third, and can undo the decision: a
destructor that stores `$this` somewhere reachable **resurrects** the object,
which is then never freed. `fibers/destructors_009` does this, and hides it
behind a `Fiber::suspend()` in the same destructor — the resurrecting
assignment has already run when the frame is given up.

## Key facts

| Fact | Why it matters |
|---|---|
| A weak reference is cleared when the target is **freed** | Not when a walk decides it is unreachable. `zend_weakrefs_notify` is on the free path |
| Freeing an object is what drops what it held | So a destructor running later in the same sweep already sees a weak reference to the earlier object cleared (`weakrefs/gh13612`) |
| Pruning a weak container is destructive | `weakRef.target = nil` cannot be put back. Everywhere else php2go can answer a weak question late and re-walk; here answering early is unrecoverable |
| Reading a weak container re-walks | `observeWeak` prunes at the read, which is what makes "no refcounts" invisible for the ordinary case |
| A freed handle goes back to the next `new` | `unit.freeHandles`. `var_dump` prints handles, so a program can see the difference |
| A destructor that throws throws at the collection point | The exception goes to whatever `try` is open *there*; the remaining destructors still run, and the last one's exception escapes with the others chained as its cause |

## Where a destructor's exception is thrown

Releasing something is an opline, so an exception from a destructor has a
position -- and it is not always the position of the opline that did the
releasing:

| Release point | Where the throw counts as happening |
|---|---|
| `ZEND_FE_FREE` at the natural end of a loop | there |
| `ZEND_FE_FREE` emitted by a `break` / `continue` / `return` leaving the loop | at the loop's *own* final free, `throw_op_num + op2.opline_num` |
| `ZEND_CATCH` with no variable (`catch (E)`) | there -- `OBJ_RELEASE(exception)`, so what the destructor throws replaces the catch body |
| the shutdown sweep | nowhere; the frame prints `[internal function]` |

The second row is the one that is easy to get wrong. The compiler emits a free
at the jump site, inside whatever `try` the jump was written in, and marks it
`ZEND_FREE_ON_RETURN` (`1 << 0`, in `extended_value`); `op2` is then a
**relative** offset to the end-of-loop free. `zend_dispatch_try_catch_finally_helper`
moves the throw there before it looks for a handler, so `break 2` out of a
`foreach (new A ...)` is caught by whatever encloses the *loop*, not by the try
the `break` sits in. Without the move the exception lands back inside the loop
that has just been dismantled -- which is what php2go did, and why the release
check looked wrong for several rounds.

## What a free lets go of

Freeing an object drops more than its storage, and each of these is observable:

| Dropped | Observable as |
|---|---|
| weak references to it | `WeakReference::get()` answering null |
| the **type sources** its properties imposed on shared boxes | `$r =& $b->prop; unset($b); $r = "foo";` being allowed |
| what its properties held | the next object's destructor running |

A type source belongs to a **slot**, not to a declaration: `clone` binds the
same box to a second object's property, and that is a second source. Removing
one when its object goes has to leave the other -- `zend_ref_del_type_source`
removes one entry, not every entry naming `Class::$name`.

Writing a variable is a release too. `ZEND_FE_FETCH` assigns into the loop
variable, so `foreach ([1, 2] as $v)` over a `$v` that held an object runs that
object's destructor at the *first* fetch, before the body has run once.

## Leaving a frame is a release point

A frame's compiled variables go when the frame does, exception in flight or
not: `zend_free_compiled_variables` runs in the leave helper. So a destructor
among them runs **before** the exception reaches the caller's catch body.

Three things have to be true of where php2go puts that release, and each is
observable:

| Placement | What it decides |
|---|---|
| after the callee's frame is off the call stack | the destructor's own trace names the *call site*, not the callee |
| after the constructor bookkeeping | `new B()` whose constructor throws gives up the half-built object first, so what it held is released at the same point |
| with the in-flight exception held as a root | no slot names the exception until a CATCH stores it, and it holds the arguments of every frame it captured |

An exception's trace keeps its **arguments** alive and not the receiver:
`$e->getTrace()[0]['args'][0]` hands an object back and its destructor must not
have run, while `$this` of the frame that threw is *not* kept alive -- which is
what lets a half-built object be destroyed at the call site.

## Where the prune belongs

Not before the destructors (it would clear an entry a resurrection should have
kept) and not after the whole sweep (it would leave an entry live for a later
destructor that the engine shows as already cleared). It belongs at the point
the object is **freed** — in the worklist, after that object's destructor,
before propagating to what it held:

```go
unit.pruneWeakTarget(ctx, object)
unit.freeHandles = append(unit.freeHandles, object.ID)
```

`pruneWeakTarget` re-walks and keeps the entry if the object came back
reachable, and only pays for the walk when a weak container actually names the
object.

## What php2go cannot answer

Recorded in [../TODO.md](../TODO.md); all of it is the same shape — a count or
an ordering that only a refcount can produce:

- `gc_collect_cycles()`'s **return value** when the cycle is made of arrays, or
  of ordinary objects holding a destructible one: Go frees those and there is
  nothing to count.
- The exact **point** a destructor runs for objects php2go keeps no list of.
- Reference markers (`&`) in `var_dump`, and private-property mangling in
  refcount-flavoured dumps.

## Pitfalls

- **Two tests can pull in opposite directions and both be right.** Moving a
  single call satisfies either one alone; only getting the *placement* right
  satisfies both. Always read the diff-set, not the total — a round that fixes
  two and breaks one still reads as progress.
- **A constructor argument is contained, not received.** In `new H(new X)`
  only `H` is the constructor's `$this`; `X` becomes H's contents and lives
  as long as H. The compiled escape analysis once read both as the receiver and
  destroyed `X` at the frame's end. Compiled output's ownership model -- which
  objects are moved and which shared, through which containers -- is in
  [../OWNERSHIP.md](../OWNERSHIP.md).
- See [serialization-and-iteration](./serialization-and-iteration.md) for the
  iterator side of generator teardown, which the collector force-closes before
  the destructor sweep.

## Giving up a `yield from` chain

A delegating generator and the one it delegates to are **one list** in the
engine, and the list's *root* is the innermost generator -- the one actually
producing values. `zend_generator_dtor_storage` releases the root before it
unwinds the generator being destroyed, so the finally blocks run
**innermost-first**:

```php
function gen1() { try { yield 1; } finally { echo "Inner finally\n"; } }
function gen2() { try { yield from gen1(); } finally { echo "Finally\n"; } }
$g = gen2(); $g->current(); unset($g);   // Inner finally, then Finally
```

Creation order is the opposite of unwind order -- `gen2`'s object exists before
`gen1`'s -- so a collector that sweeps its object list in order gets this
backwards. `closeDelegatingGenerator` collects the chain top-down and closes it
bottom-up, stopping at a delegate that is still reachable.

## `__clone` is a frame

Cloning is a call: `NoClone->__clone()` appears in a backtrace at the position
of the `clone` expression, with the clone site's caller above it. The frame is
named for the **declaring** class, so a child that inherits its parent's
`__clone` shows `Parented->__clone`. Two other things that look like plain
operations and are also frames: a property hook, and a destructor at the
collection point.

## A lexical binding ends with the frame that made it

Reference sharing has a scope lifetime independent of object reachability. A
normal generated function releases the holders it created with `=&` when its
frame exits. It must not release a by-reference parameter: that binding was
created by the caller and survives the callee. A generator is different again;
its lexical frame survives the Go invocation that yielded, so its holders are
released only when the generator frame is closed.

The compiled path finds locally-created bindings from `bind_ref` targets and
defers `php.Unshare` for those slots. `unset` uses the same operation at the
statement boundary. Applying it to every reference-valued local would erase
callers' bindings and make `var_dump` holder markers disappear too early.
