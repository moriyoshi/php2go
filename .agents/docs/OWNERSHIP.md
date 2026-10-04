# Ownership tracing: containers, containment graphs, moves and shares

Written 2026-09-25. Design for the analysis that replaces "does this object
outlive its frame" with "who owns this object, and how did ownership get
there". Status and measurements are at the end and are kept current; the
design above them is the part that should change slowly.

Read [IR-DESIGN.md](./IR-DESIGN.md) for the IR's shape -- places versus values,
`call.intrinsic` spelling, the SSA rule that a value never outlives its block --
before this.

## Why the frame question is the wrong one

The compiler plan's D3 phase was scoped on a premise: most objects do not
escape, so refcount only the ones that do. It was measured before anything was
built (Phase A, item 6), on the first real targets:

| | Twig `src/` | CakePHP `src/` | `Zend/tests` (sample) |
|---|---|---|---|
| `new` sites | 669 | 879 | 1,761 |
| confined to their frame today | 34 (5.1%) | 19 (2.2%) | 45 (2.6%) |
| ceiling if returns were not escapes | 94 (14.1%) | 132 (15.0%) | -- |
| in the file body, not asked | 0 | 1 | 1,107 (62.9%) |

The premise does not hold: ~95% of allocations in real library code escape the
frame that made them. `Zend/tests` hid this because two thirds of its
allocations are at top level, where frame confinement is not a question at all.

Why they escape, counting sites that do exactly one thing with the object:

| sole cause | Twig | CakePHP |
|---|---|---|
| put into an array | **163** | 8 |
| passed to a method | 69 | **115** |
| the return path | 55 | 94 |
| stored into a property | 22 | 42 |

And where the *container* goes, for objects stored into one:

| container's fate | Twig | CakePHP |
|---|---|---|
| returned | **146 (76%)** | 3 (8%) |
| passed to a call | 13 (7%) | **23 (58%)** |
| came from a property | 5 (3%) | 7 (18%) |
| stays in the frame | 29 (15%) | 7 (18%) |

So an object's lifetime is its **owners'** lifetime, and the owners are
containers -- arrays, other objects, the receiver -- that themselves move, get
shared, and die. `return [new ApplyTokenParser(), new ForTokenParser(), ...]`
is Twig's shape: the objects never escape on their own; the array does, taking
them with it.

The frame question also produced a **soundness bug**, found while writing this:
`escapes.value` treated any mention in a `call.ctor` as the receiver, so
`new Holder(new X)` confined `X` even though `Holder`'s constructor stores it.
The emitter destroyed `X` at the frame's end while the returned `Holder` still
held it. A constructor argument is a containment edge, and an analysis without
the concept had no way to see it.

**Fixing it took Twig's confined count from 34 to 1, and CakePHP's from 19 to
14.** So the 5.1% in the table above was almost entirely the bug: 33 of Twig's
34 claims were objects handed to a constructor that kept them. The table is left
as measured; the corrected figures are in the status section.

## The model

### Identities and owners

An **identity** is one object or one array. Every SSA value, every place (`$x`,
`$__tmp`), and every container slot holds at most one identity at a time.

An **owner** of an identity is anything that keeps it alive: a local, a
parameter slot, an array holding it as an element, an object holding it in a
property, the receiver `$this`, a static, the caller (through a return), or
something this analysis cannot see.

An identity dies when its last owner does. Destruction timing -- the thing the
corpus grades byte for byte -- is therefore a property of the ownership graph,
not of any one variable.

### PHP's two ownership semantics

PHP has both, and the IR does not mark which one an operation has:

- **Objects are handles.** Assigning, passing, returning or storing an object
  copies the handle. Every copy is another owner of the *same* object: this is
  **shared ownership**, and it is the default for objects.
- **Arrays are values.** `$b = $a` makes a second array, observationally. But the
  elements are copied by handle, so every object *inside* `$a` is now owned by
  both arrays. Copying a container **shares its contents** without any store
  appearing in the IR -- the one edge kind invisible at the append.

A **move** is a transfer whose source dies at the transfer. It preserves unique
ownership, and unique ownership is what makes a death point static. The IR
shows moves directly in two places:

- an SSA value with exactly one use is consumed by that use;
- a terminator (`ret`, `throw`, `ret.yield`) transfers out of a frame that dies
  at that instruction.

Anything else is a **share** unless proven otherwise. The safe direction is
share: calling a share a move under-counts owners, and under-counting owners
frees an object someone still holds.

### Edge classes

Every operation relates its operands to its result and to the containers it
writes. There are seven classes.

| class | meaning | examples |
|---|---|---|
| **forward** | the result *is* the operand's identity | `read_var`, `write_var`, `copy`, `verify_return`, `coerce_return`, `iter.new` |
| **contain** | the operand becomes an element of a container | `dim.append`, `dim.set`, `prop.set`, `array.append`, `array.set`, constructor arguments |
| **extract** | the result is *some* element of a container | `dim.get`, `prop.get`, `iter.current` |
| **part-of** | the result is a place inside a container | `prop.ref`, `dim.ref`, `dim.append.ref` |
| **observe** | the operand is read and nothing is retained | arithmetic, comparisons, `count`, `strlen`, `is_set`, `type_check` |
| **re-entering observe** | observe, but the operation may call user code with the operand as `$this` | `to.string` (`__toString`), `echo`, `concat` on an object |
| **sink** | the operand goes somewhere this analysis does not follow | static properties, reference bindings, dynamic calls, unknown operations |

Value-producing container operations carry **two** edges at once:
`%2 = array.append(%1, %0)` forwards `%1` to `%2` *and* contains `%0` in `%2`.
Missing the forward edge is how a measurement harness reported 92% of Twig's
containers staying in the frame when 76% are returned.

### Soundness rules

1. **Unknown means sink.** An intrinsic or opcode with no declared class is a
   sink for every operand it mentions. The failure the measurement harness hit
   three times in a row -- a missed read, a missed value-producing append, a
   missed return coercion -- defaulted to *confined*, which is the unsafe
   answer. A missing entry here costs precision, never correctness.
2. **The table is declared once and checked for completeness.** Every name the
   lowering can emit -- `intrinsicFor`'s keys, every variant in
   `php.Catalogue()`, and the variable intrinsics -- has a declared class, and a
   test fails when one does not. Adding an intrinsic forces a decision instead
   of silently becoming a sink.
3. **Re-entry comes from the catalogue, and from the table where the catalogue
   is silent.** Whether an intrinsic can call user code is `Effects & ReEnters`
   on the catalogue variant the IR names. User code receiving an object as
   `$this` can store it, so a re-entering operation is a sink for every operand
   (`reenter:<name>`). The catalogue's flag was set for diagnostic ordering and
   **misses ArrayAccess**: `dim.get`, `dim.set`, `dim.append` and the `dim.ref`
   family are unmarked, though on an object each is `offsetGet`/`offsetSet`. The
   table marks those itself. Which user code runs depends on the class, which
   the trace does not know, so the caller answers for the whole unit
   (`OwnershipOptions.ReEntry`): php2gotest says "keeps" when any compiled
   `__toString`, `__get`, `offsetGet`, iterator method or get hook leaks
   `$this`, or when the unit declares `__set`, `offsetSet` or a set hook at all,
   since those take an argument and there is no argument summary. Nil means
   "keeps".
4. **Call summaries decide calls.** `keepsArg` per position for functions,
   `argKept` per method name, `leaksThis` per method for receivers -- the same
   predicates the existing analysis takes. A constructor argument is
   `argKept("__construct")`, which is "keeps" for every compiled class today.
5. **Share unless a move is provable.** Flow-insensitive owner counting
   over-states sharing; it never under-states it.

### Computing it

Flow-insensitive, per function, in one walk and one query per site. Two
union-finds over the same nodes -- values, places, and contents nodes:

1. **The escape partition** is Steensgaard's analysis specialised to ownership.
   *forward* and *alias* join two nodes; each class gets one **contents** node,
   a field-insensitive summary of its elements. *contain* joins the value into
   the container's contents; *extract* and *part-of* join the result into it;
   *splice* joins two contents nodes. Joining two containers joins their
   contents, so a cycle is harmless. Joining every element of a container into
   one node is imprecise (all elements share a fate) and sound (none is lost).
2. **The identity partition** follows *forward* and *alias* only, and owners are
   counted on it. Counting on the escape partition would make every element of
   a container an owner of every other: `[new A, new B]` is two objects with one
   owner each, not one object with two.
3. **Fate** is a breadth-first walk up the containment graph from the site's
   escape class: every class reached contributes its reasons, and the first one
   with a reason gives the chain -- `X` escapes because it is contained in
   `%3`, which is returned. An allocation with a reason of its own has an empty
   chain.

**Owners** are the distinct locals in the identity class, plus each *contain*
edge and each non-terminal sink out of it, plus one more when the allocation is
held by a container something was read out of -- that read may be another owner,
and which element it took is not known. A terminal sink (`ret`, `throw`) moves
rather than adds an owner, because the frame dies with it. **Shared** is more
than one owner.

Per allocation site the result is: its fate (confined, or the set of reasons it
escapes), the containment chain to the first escaping owner, how many distinct
owners it had, whether ownership was ever shared or only moved, and whether it
is ever written in place.

### Sites, writes and moves

The copy questions in OWNERSHIP-PERF.md need more than fates, and each of them
is an extension of the same graph rather than another analysis
(`lower.Trace` returns them all):

- **Sites** are every allocation this body makes: `new`, `clone`, `array.new`,
  an **array literal written inline** as an operand (`write_var($a, [])`, which
  gets a synthetic node), and the result of a call to a body known to return
  something fresh.
- **Written** is on the identity partition: an intrinsic declares which
  arguments it changes in place (`Writes` in the table) -- an element set, an
  append, a place taken for writing, a by-reference `foreach`. A by-reference
  send, a by-reference closure capture, and any sink the trace cannot follow (a
  static, a global, an unknown operation) write too. **Building is not
  writing**: an `array.append` whose container dies at that instruction is the
  array under construction. Replacing what a variable holds does not write the
  old value, and a parameter or a call's result is not written by whoever else
  holds the original -- they were handed over by value.

  A **`preferref` send** -- every method call's arguments, since the callee is
  not known when the call is compiled -- writes only if the callee's parameter
  is by reference (`OwnershipOptions.ByRefArg`). php2gotest answers from a
  compiled body's parameter list, a builtin's harvested signature, or every
  compiled method of that name; an unknown callee is by reference. Counting
  every `preferref` as a write had made 49% of a Twig sample's parameters look
  written; answering it, 15.7%, and what remains is mostly methods declared in
  files this compile does not see.

  It is **not** on the escape partition, because storing into a container
  copies: `$this->nodes = $nodes` gives the property its own array, so writing
  through the container is not writing what was stored. On the escape partition
  every parameter a constructor put into `$this` became written the moment any
  property was, which was half of Twig's parameters. The rule depends on the
  runtime actually copying at every container store; the property and
  static-property stores did not, which is how this was found (see the
  journal, 2026-09-26).
- **Borrowed** is on the identity partition: an identity that may hold
  something this body did not make -- a parameter, `$this`, an element read out
  of a container, an unknown call's result.
- **Liveness** is ordinary backward dataflow over SSA values, with the exception
  edges folded in (every block in a region reaches its handlers and all the
  enclosing ones). It says where a value dies, and which values are still live
  after each instruction.

From those:

- a **move** is a `write_var` whose value is a site, not borrowed, dies at the
  store, has no owner but the destination -- and has **no other name still
  live**: `%2 = copy(%1)` makes two names for one array, and `%2` dying says
  nothing about `%1`;
- an **in-place append** is a `concat` whose left operand is not borrowed, dies
  there, is held by no local, and has no other live name except the concat's
  own result (a loop joins the two through its block parameter). A container or
  a callee may hold it: `lower.StringChains` hands those a finished snapshot,
  and a buffer never rewrites bytes it has handed out;
- **returns fresh** is a body whose every return is not borrowed and escapes
  only by being returned -- an allocation, or a value an operation computed;
  never a generator or a reference return;
- **parameter written** is the escape class of each `recv`;
- **parameter releasable** is a `recv` whose holder never leaves the frame as
  itself (reasons only `param` and the sends that share into the callee), so
  its share on the caller's array is handed back at exit;
- **release after** is a promoted copy whose uses all read it, pass it on or
  store it, released after each instruction where liveness says it dies;
- **private** is an allocation this body made, not borrowed, never written, and
  with no escape at all, not even a return. It licenses three emitter choices
  (`SharedLiterals`, `SharedCopies`, `SharedStores`): build a literal once and
  share it, skip a `copy`, and store without copying. The escape clause is
  strict because a shared array that reaches a caller can be iterated by
  reference there, where this body cannot see it.

The last two are per-body summaries, and a caller consumes the first:
`OwnershipOptions.FnReturnsFresh` / `MethodReturnsFresh` make a call to a fresh
body an allocation site. php2gotest computes them to the least fixpoint --
starting from "not fresh", since a body's answer only improves as its callees'
does -- and a method name is fresh only when every compiled body of that name
is.

## What it is for, and what it is not yet

It is for **deciding where reference counting can be elided** and **where a
death point is static**:

- an identity that is confined and uniquely owned dies at a statically known
  point, with no refcount at all;
- an identity that escapes but is only ever *moved* has one owner at a time,
  so its refcount never exceeds one and the traffic can be elided;
- a shared identity needs a real count.

The first version is **analysis and measurement only**. It does not change what
the emitter destroys and when -- that is D3.3, and it will move the corpus in
both directions, so it lands separately with its own before-and-after.

Known imprecision, in the order it costs precision:

- **One file at a time.** `php2gotest` compiles one file per program, so a
  method declared in another file has no body to summarise and `argKept` says
  "keeps". This is the largest single term: "passed to a method" is CakePHP's
  top escape cause, and much of it is an unknown callee rather than a real
  escape. Whole-program compilation is the fix, and it needs the bundle path.
- **No per-method argument summary.** A constructor that only stores its
  argument into `$this` is still "keeps", so the argument is a sink rather than
  contents of the new object. The precise edge is *contain into the receiver*,
  and it needs a summary of the form "argument *i* reaches only `$this`".
- **Parameters are owned by the caller.** A by-value array parameter is really
  a local copy; treating it as caller-owned is conservative.
- **Flow-insensitive owner counting** over-states sharing, e.g. an object staged
  through a local that is dead by the time it is stored.
- **An array copy is treated as the same identity.** `$b = $a` copies an array,
  and the trace joins the two, so a store into `$b` looks like a store into
  `$a`. That is a superset of the truth -- copying shares the elements, not the
  array -- and it is the safe direction.
- **Re-entry is answered for the whole unit, closed-world.** The same
  assumption `leaksThis` makes by answering from compiled bodies; a class from
  another file with a `__toString` that stores `$this` is not seen.

What it can buy in speed, measured before building any of it, is planned in
[OWNERSHIP-PERF.md](./OWNERSHIP-PERF.md).

## Status and measurements

Kept current; newest first.

### 2026-09-26 -- sites, writes, moves and summaries

`lower.Trace` returns the sites (with array literals and fresh call results),
**Written**, the **moves**, the **in-place appends**, **returns fresh**, and
**parameter written**; `ownership_live.go` is the liveness they rest on.
php2gotest prints an "array and string facts" block and, with
`PHP2GO_ESCAPE_DUMP=1`, one `MOVE` / `APPEND` line per finding.

Same Twig and CakePHP trees, one file per program:

| | Twig | CakePHP |
|---|---|---|
| array sites (`array.new` + inline literal) | 493 (170 + 323) | 2,231 (1,135 + 1,096) |
| never written in place | **375 (76%)** | **1,616 (72%)** |
| confined | 9 (1.8%) | 291 (13%) |
| `write_var` stores | 2,490 | 11,553 |
| moves -- no copy needed | 27 (1.1%) | 322 (2.8%) |
| concatenations | 319 | 1,667 |
| appends in place | **166 (52%)** | **617 (37%)** |
| bodies returning fresh | 749 of 1,209 (62%) | 1,581 of 3,770 (42%) |
| parameters written | 417 of 1,433 (29%) | 1,510 of 4,884 (31%) |

What the numbers say for OWNERSHIP-PERF.md:

- **Appends in place (P1) have the widest reach.** Half of Twig's
  concatenations could extend their left operand; the dump shows node
  `compile()` methods such as `ForNode::compile` among them.
- **Three quarters of arrays are never written** after they are built. That is
  P3a's headroom (share, do not copy), and why its static half is worth doing
  before the runtime bit.
- **Moves (P2) are rare in library code** as measured today: 1-3% of stores.
  A store's value is usually borrowed -- a parameter, a property read, the
  result of a call into another file. A whole-program compile would turn more
  of those calls into fresh results; one file at a time, P2 is the smallest
  lever.

Two correctness bugs in compiled output fell out of settling the rules, both
fixed with a differential case, and neither moves the corpus
(bh3: 2606 / 921 / 379 / 1 against bh1's 2608 / 921 / 379 / 1, the two
differences being the known `random_int` tests):

- `foreach ($a as &$v)` over a local in a function promoted `$a` and lost the
  loop's writes (`foreach_by_ref_local.php`);
- a property or static-property store did not copy an array
  (`property_store_copies.php`).

### 2026-09-25 -- first implementation

`lower.TraceOwnership` in `internal/lower/ownership.go`, with the edge table
`ownershipRules`. `TestOwnershipRulesAreComplete` holds the table to the
catalogue in both directions: every variant has a rule, and every rule names a
variant. `php2gotest` reports the trace after its escape tallies and, with
`PHP2GO_ESCAPE_DUMP=1`, prints one `OWN` line per escaping site with its reasons,
chain and owner count. The ad-hoc classifier it replaces is gone.

Same corpora and commits as the motivation tables (Twig `src/` @9d33a10,
CakePHP `src/` @39a15f5), one file per program, `new` sites outside the file
body and suspending bodies:

| | Twig | CakePHP |
|---|---|---|
| `new` sites | 669 | 878 |
| confined by the trace | 2 | 20 |
| confined by `ConfinedNew` after the ctor fix | 1 | 14 |
| escaping, only ever **moved** | 497 of 667 (75%) | 676 of 858 (79%) |
| escaping only through a container | 167 (25%) | 38 (4%) |

Sole reasons, for sites with exactly one:

| sole reason | Twig | CakePHP |
|---|---|---|
| `return` | **212** | 118 |
| `throw` | 177 | **382** |
| `method.arg` | 55 | 52 |
| `method.this` | 28 | 87 |
| `opaque` (body uses `extract` etc.) | 0 | 25 |

What the numbers say:

- **Three quarters of escaping objects are only ever moved.** One owner at a
  time: built, maybe put into one container, handed out by a return or a throw.
  A refcount on those never exceeds one, which is the case the plan's tier 2 can
  elide. This is the figure that rescues D3's premise in a different form: not
  "most objects do not escape" but "most objects that escape are not shared".
- **`throw` is CakePHP's largest single reason, and nothing measured it
  before.** The old classifier looked only for `ret` in terminators. `throw new
  FooException(...)` is a pure move out of the frame into the unwinder.
- **Twig's containers are returned.** 149 of the 167 sites that escape only
  through a container do so because the container is returned -- the
  `getTokenParsers(): array { return [new ...]; }` shape. Inlining or a
  caller-side trace is what would confine them.
- **The trace confines what the frame question cannot.** Every disagreement
  with `ConfinedNew` is in the trace's favour (1 on Twig, 6 on CakePHP): an
  object put into a frame-local array that is only counted or iterated. None
  goes the other way, so nothing `ConfinedNew` claims is refuted by the trace.
