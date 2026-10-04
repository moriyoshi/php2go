# Performance from ownership: a plan

Written 2026-09-25. What the ownership trace (`OWNERSHIP.md`,
`lower.TraceOwnership`) can buy in compiled output's speed, sized by measurement
before any of it is built. Each phase says what the trace has to prove, what
the emitter and runtime do with the proof, how it could go wrong, and the number
that says whether it worked.

## Where compiled output's time goes today

Compiled with `php2gotest -keep`, built against the local runtime, and timed
next to the embedded engine (`php2eval -raw`) on the same file. `Zend/bench.php`
and `micro_bench.php` are vendored in `third_party/php-src`; the copies used
here drop the `date_default_timezone_set` call, which compiled output refuses.
Profiles are Go's CPU profiler over a time window, since `perf` is not permitted
on this box. The inputs are in `.agents-workspace/tmp/bench/`.

**`php2go bundle` is not compiled output.** It embeds the program and runs the
interpreter; its first profile was all `interp.execFrame`. Every number below
is from the `gogen` path.

### `Zend/bench.php`: 18.1 s compiled, 1.19 s engine (15x)

| | compiled | engine | ratio |
|---|---|---|---|
| `strcat(200000)` | 10.98 | 0.008 | **~1400x** |
| everything else | 7.10 | 1.18 | 6.0x |

`strcat` is `$str .= "hello\n"` in a loop, and it is quadratic: the profile is
`runtime.concatstrings` -> `memmove` plus the GC churning the discarded copies.
The builder-promotion pass (`builders.go`) exists for exactly this shape and
reports **0 candidates**, because promotion had already turned `$str` into a
loop-carried SSA value (`v12 = v5; goto bb2`), and the pass looks for a
variable.

The remaining 6x is **operator dispatch**, not ownership:
`IsSmallerValues` 22% cumulative, `compareAt` 14%, `Add` 10%, `Increment` 7%,
and `noticeObjectComparison` / `isEnumCase` showing up on integer compares.

### Isolates

| shape | compiled | engine | ratio | where the time goes |
|---|---|---|---|---|
| `$x = $val ?: null`, `$val` a 10-element literal (`issetor`) | 16.88 | 0.18 | **93x** | the literal is **rebuilt every iteration** (`func() { a := NewArray(); a.Set... }`) and then deep-copied by `CopyValue` |
| `$x = $f ? null : $val` (`ternary`, micro_bench) | 13.14 | 0.14 | **96x** | same |
| `$x = $_GET` (micro_bench) | 3.35 | 0.19 | 18x | `StoreVar` deep-copies the array |
| `$x = new Foo()` in a loop | 8.43 | 0.47 | 18x | allocation: the property map (`growToSmall`, `Object.Declare`) and the GC, 40% of samples in mark workers |
| `$b = build(100)` returning a fresh array | 13.03 | 0.63 | 21x | appending to a hash-indexed array (34%), **cloning the returned array at `StoreVar` (12%)**, GC |
| `sum($a)` iterating a by-value array | 5.91 | 0.74 | 8x | iteration (`cursorOf`) and arithmetic; **no copy** -- promotion removed the parameter's store |
| `$this->x++` | 2.92 | 0.14 | 20x | hashed property lookup and the generic place/set path |

### What that says about the trace

- **Ownership is the lever for the largest ratios**, and they are all the same
  mistake: a value is copied or rebuilt while nothing else can see it. The
  engine avoids it with a refcount; compiled output has none and copies
  eagerly (`Value.Copy`, `Array.Clone`: a full recursive rebuild with a fresh
  hash index and a `seen` map, every time).
- **It is not the lever for the broad 6x.** Operator dispatch, property access
  through a hash, and a hash-indexed representation for list-shaped arrays
  are type specialization (plan D1/D4), object shape, and a packed-array
  representation. They are listed at the end so the ceiling is honest:
  **after strcat, bench.php is ~7 s against 1.2 s, and ownership moves little
  of that.**

## The facts the trace has to supply

**Supplied, 2026-09-26** -- `lower.Trace`, described in `OWNERSHIP.md` under
"Sites, writes and moves". What was planned, and where the build departed from
it:

1. **Sites for every array, not only `new`**: `array.new`, an inline array
   literal (given a synthetic node), and a call to a body known to return
   something fresh. Strings did not need sites: the append question is about
   values, not allocations.
2. **Written**, per escape class, declared per intrinsic (`Writes`) and
   checked by a test for every intrinsic that changes a container. Building an
   array is not a write; neither is replacing what a variable holds.
3. **Linear use** turned out to need **liveness**, not a use count. The
   loop-carried string in bench.php's `strcat` has two uses -- the concat in
   the body and the `strlen` after the loop -- on paths that never meet. And a
   value that dies is still not movable if **another name for the same
   identity** is live: `%2 = copy(%1)`. Both are in the implementation.
4. **Returns fresh**, as a least fixpoint over the unit; it replaces nothing
   yet (`ReturnsFreshObject` still decides object releases).
5. **Parameter written**, per `recv`.

Running the new rules' tests found **a miscompile in promotion** that had nothing
to do with the trace: `foreach ($a as &$v)` over a local in a function. The
loop needs `$a`'s storage box, `ExplicitReads` had turned the argument into
`read_var($a)`, so promotion could not see the address being taken, promoted
`$a`, and `return $a` answered the array from before the loop. Fixed in
`mem2reg.go`; `foreach_by_ref_local.php`.

Settling where **Written** lives found a second one: **a property store did not
copy.** `$o->a = $arr; $o->a[] = 2;` grew `$arr`, and a later `$arr[] = 3`
showed up in `$o->a`; static properties the same. The dimension and array
stores copied; `propSet` and `staticSet` stored the pointer. Fixed, with
`property_store_copies.php`. It adds a clone per array stored into a property,
which is correctness first and exactly the copy P2's moves exist to remove -- so
P2 should cover property stores as well as `write_var`.

## Phases

In order of payoff over effort. Every phase is a **pure performance change**:
corpus and fixture counts must not move, and any movement is a regression to
explain, not progress. Every elision is a claim that nobody can observe the
missing copy, so it owes differential cases built to break it (the list is at
the end).

### P0 -- A benchmark harness

**Done, 2026-09-26.** `hack/bench.sh` (usage in TESTING.md), with
`php2gotest -emit DIR` writing each program as a module, the isolates in
`hack/bench/`, and `hack/bench/prof.go` as the profiling hook behind a
`benchprof` build tag. Baseline, median of 3, wall clock:

| program | compiled | engine | ratio |
|---|---|---|---|
| `zend_bench` | 19.49 | 1.21 | 16.1x |
| `zend_micro_bench` | 85.50 | 6.16 | 13.9x |
| `arrpass` | 16.87 | 1.38 | 12.2x |
| `issetor` | 14.52 | 0.20 | 73.3x |
| `newfoo` | 10.54 | 0.49 | 21.4x |
| `propinc` | 2.98 | 0.14 | 21.3x |
| `strcat` | 12.04 | 0.017 | 717x |

Run to run the compiled numbers move by up to ~10% on this box (issetor
14.5 -> 16.4 s between two runs with no change that touches it), so a change
smaller than that is not a result.

Nothing builds compiled benchmarks today; the numbers above took `-keep`, a
hand-copied `go.mod` and a profiler file dropped into the build directory.

- `hack/bench.sh`: for each input, compile through the `gogen` path, build,
  run N times, run the engine on the same file, print compiled / engine /
  ratio, and optionally write a CPU profile over a window
  (`PROF_OUT`/`PROF_START`/`PROF_DUR`, the file in
  `.agents-workspace/tmp/bench/zz_prof.go.txt`).
- Inputs: the two Zend benchmarks (timezone call stripped), the isolates, and
  one real workload -- a Twig template render once the bundle path compiles
  rather than interprets.
- Record the median of 5, and quote only what survives the noise
  (`TESTING.md`).

Done when the tables above reproduce from one command.

### P1 -- In-place string append (bench.php's strcat, ~1400x)

**Done, 2026-09-26.** `lower.StringChains` (`internal/lower/strchain.go`), run
by php2gotest after specialization. Measured with `hack/bench.sh`:

| | before | after | engine |
|---|---|---|---|
| bench.php's own `strcat(200000)` line | 11.83 s | **0.033 s** | 0.008 s |
| `zend_bench` total, wall clock | 19.49 s | **7.43 s** | 1.21 s |
| `strcat` isolate, wall clock | 12.04 s | **0.051 s** | 0.015 s |

bench.php went from 16.1x the engine to 6.2x, which is the operator-dispatch
floor the table below predicted. Corpus `bh4` against `bh3`: unchanged
(the one file that moved is the `random_int` flake). Fixture
`string_append_chains.php`, mutation-checked: letting an int enter a chain, or
dropping the liveness checks, each make it fail.

How it departed from the plan: the chain is carried in `*strbuf` and every
other use reads an O(1) **snapshot** (`strbuf.finish` over a Go
`strings.Builder`, which only ever writes past what it has handed out). That
makes a snapshot safe to hand anywhere -- a container, a callee -- so the trace's
append rule needs no "no other holder" clause, only no *local* and no other
live name. The group of values one buffer can flow through is checked as a
whole, and a chain is entered only from a string literal or a concatenation,
so no conversion moves.

**Trace fact.** A string identity whose values form a *linear chain*: each is
consumed by exactly one `concat` as its left operand (or passes through a
block parameter to one), and every other use is an observe (`strlen`, `echo`,
a comparison). No other owner.

**Emitter.** Carry such a chain as the existing `*strbuf` representation:
`strbuf.new` at the chain's start, `strbuf.append` at each concat,
`strbuf.finish` where it leaves the chain. `builders.go` already has the
representation and the lowering; this changes what *selects* a candidate --
the trace's linear-use fact over SSA values, instead of a variable-shaped
pattern that promotion erases first.

**Expected.** strcat from 10.98 s to near the engine; bench.php from 18.1 s to
~7.2 s. Measured by P0.

**Risks.** A read of the buffer mid-chain must not destroy it (`builders.go`
notes Go's builder can be read); a `.=` through a reference or a property is
not a chain (the place is not the frame's alone); a `__toString` operand is an
observe that can re-enter -- the right operand, not the chain.

### P2 -- Do not copy what nobody else holds (the `StoreVar` copy)

**Done, 2026-09-27**, and after P3b it matters in a different way than
planned: a store no longer clones, it shares -- which raises the body's holder
count even for a fresh temporary that dies at the store, so the variable's
*next write* cloned an array nobody else held. The trace's `Moves` (fresh, not
borrowed, dies at the store, no other holder or live name) now take
`StoreVarShared`, the store that does not share. `hack/bench/movewrite.php`
(`$b = build(200); $b[] = $i;` in a loop) 4.72 s -> **3.56 s**; `arrpass` 13.9 s
-> 13.0 s. Fixture `move_stores.php`: a property's array, a function that keeps
what it returns in a static, a function returning its parameter -- none of them
is a move, and treating every store as one breaks it.

Container stores (`$list[] = build()`) still share a fresh value; that costs
one extra copy per container, not one per iteration, so it is left for later.

**Trace fact.** At `write_var($x, v)`, `v` is a **move**: a fresh site (a
literal, `array.new`, a call to a body that returns fresh) with no other
owner, or an SSA value with no later use whose identity has no other owner.

**Emitter.** Emit a store without `Copy` -- a `WriteVarMoved` intrinsic next to
`StoreVar`, so the choice is visible in the IR rather than a flag. The typed
reference path (`CoerceRefWrite`) keeps its copy.

**Expected.** The 12% clone in `$b = build(100)`, and every store of an array
literal or a fresh call result. Modest per site, broad in library code: Twig's
top escape reason is an array being returned, and its caller stores it.

**Risks.** The eager copy is also where php2go **flattens references**
(`Clone` keeps a reference only if two names hold it) and **carries the
internal pointer**. A moved array has had no second name, so neither changes,
but both need attack cases. A fresh-return summary that is wrong -- a callee
that returns `$this->items` -- would alias a property; that summary is the
riskiest single input and gets its own cases.

### P3 -- Share what nobody writes (issetor and ternary, ~95x)

Two steps, because the static half covers the benchmark and the runtime half
covers escaping values.

**P3a is done, 2026-09-27.** Three facts from the trace, each consumed by the
emitter: `SharedLiterals` (an inline literal built once, as a package-level
array, program-wide by text), `SharedCopies` (a `copy` not made) and
`SharedStores` (a `write_var` that calls `StoreVarShared`, StoreVar minus the
copy). All three rest on one predicate, **private**: an allocation this body
made, not borrowed, never written in place, and with no escape at all -- not
even a return, because a caller holding the one prebuilt array could iterate it
by reference. Measured with `hack/bench.sh`:

| | before (P1) | after | engine |
|---|---|---|---|
| `issetor` | 16.35 s | **0.73 s** | 0.21 s |
| micro_bench `$x = $f ? $f : $a` (net) | 13.02 s | **0.11 s** | 0.14 s |
| micro_bench `$x = $a ?: null` (net) | 8.58 s | **~0** | 0.13 s |
| micro_bench `$x = $hash['v']` (net) | 1.80 s | **0.40 s** | 0.19 s |
| `zend_micro_bench` total | 83.9 s | **61.1 s** | 6.15 s |

Corpus unchanged (`bh5`, `bh6`: only the `random_int` flakes move). Fixture
`shared_literals.php`; dropping either half of **private** -- the write check or
the escape check -- makes it fail, but only after two by-reference loops were
added: every other shape was protected anyway by the copy a store makes, so
the first version of the fixture could not tell a sound rule from an empty one.

The static half was specified as literals only; the ternary's merge temporary
showed the store is the other half of the same cost, and it is the same proof:
the destination is in the value's identity, so "private" already covers it.

`$x = $_GET` (3.7 s, 18x) is untouched: `$_GET` is not this body's, so it
needs P3b.

**Correction, 2026-09-27: that number was never a copy.** Neither back-end
created `$_GET` at all, so every iteration warned `Undefined global variable
$_GET` into micro_bench's output buffer, and the warning was the 3.7 s. With
the superglobals created (`LTM/variable-fetch-diagnostics.md`) the loop is
0.14 s net against the engine's 0.19 s. A benchmark row that is slow for a
reason nobody profiled is a correctness question first.

**On library code P3a reaches little.** Twig: 4 literals shared, 1 copy and 1
store not made (of 495 array sites, 2,490 stores). CakePHP: 256 literals, 9
copies, 25 stores (of 2,379 sites, 11,553 stores). The 72-76% of arrays that
are *never written* mostly leave their frame -- returned, passed, stored -- and
private refuses any escape. That is the case for P3b: a run-time
separate-on-write bit covers exactly the shares a static proof cannot bound,
and it would also fix the parameter aliasing bug in TODO.md.

**P3a, static, as planned.** A literal array whose identity is never **Written**, and whose
values do not reach a sink the trace cannot see (a by-reference send, a
property, a return), is built **once** -- a package-level value -- and read
without `CopyValue`. That also stops the per-iteration rebuild, which is the
larger half of the cost: the lowering folded `$val` into its literal, and the
emitter rematerialises a literal wherever it is used. The trace fact is
exactly "Written = false and no escape", on the identity that holds the
literal and every local it was copied into.

**P3b is built, 2026-09-27** -- as a header/body split rather than a bit on
the array, because a bit cannot separate: the writer has to swap in a copy, and
it may not know the slot it came from. `php.Array` is now a header (the
holder's internal pointer and foreach cursors) over an `arrayBody` (the
elements) that headers share. `Share` gives a new holder a header over the same
body; every mutator calls `own`, which gives its header a body of its own if
the one it has is shared. Nothing outside `array.go` touches the body, so that
file is the whole audit, and `TestShareSeparatesOnEveryMutator` lists every
mutator.

- **Compiled code only shares.** `StoreVar`, the dimension and literal
  stores, the emitted property and static stores, and parameter receipt
  (`Unbox`) share; the interpreter still `Clone`s, so for it every body is
  unshared and `own` is one branch.
- **References are decided at separation**, as `zend_array_dup` decides them:
  kept when another name holds the box or when it points back at the array,
  flattened otherwise. The eager Clone decided at assignment and got
  `$r = &$a[0]; $b = $a; unset($r); $a[0] = 7; echo $b[0];` wrong (7; PHP 1).
- **A write in progress blocks sharing.** `$b[0][0] = $b` opens `$b[0]` for
  writing before it reads `$b`; `DimTarget` counts open writes on the body,
  and `Share` takes an eager snapshot while any is open (`bug38469`,
  `gc_032`).
- **Identity is the body's.** Cycle detection -- `var_dump`, comparison,
  `Clone`, and `array_id`, which the userland `print_r` walks with -- keys on
  the body, or a self-referencing array seen through two headers recursed
  forever (`bug69376_2`).
- **Promotion kept its copy.** A promoted `$b = $a` had no store left to copy
  in, so `$b` was `$a`'s own array; the promoted value is now marked `share`
  where it is read out of storage, and shared there.

Three pre-existing wrong answers are fixed by it: a by-value parameter aliasing
the caller's array, a promoted copy aliasing its source, and the reference
timing above. Fixture `array_value_semantics.php`, mutation-checked three ways
(a mutator without `own`, `Unbox` not sharing, no `share` mark).

**Handing shares back.** A share that is never returned makes the next writer
copy for a holder that no longer exists. As first built, `shared` was a
one-way flag, and receiving an array parameter set it: `hack/bench/passloop.php`
-- append to an array, pass it to a function that only counts it, every time
round -- ran in **26.15 s against the engine's 0.002 s**, one full copy per
iteration. The flag is now a count of extra holders: separating takes the
writer's one away, and two trace facts hand back shares the compiler proved
dead:

- `ParamReleasable`: the holder a by-value parameter arrived as never leaves
  the frame as itself -- passing it on, storing it and assigning it all share
  into holders of their own; returning, yielding, a by-reference capture or an
  unknown operation do not. The emitter defers `php.ReleaseShared` at receipt,
  and for the parameter's slot too, which the store into it shared again.
- `ReleaseAfter`: a promoted copy (a `share` read) whose every use reads it,
  passes it on or stores it is released right after the instruction where
  liveness says it dies. Never on a terminator and never before the last use.

`passloop` 26.15 s -> **0.026 s**; `copyloop` (the same with `$copy = $arr`
in the loop) runs in 0.02 s where the engine takes 1.2 s -- PHP keeps `$copy`'s
reference until it is overwritten, and copies on every append.

Fixture `read_only_params.php`. Releasing at every instruction rather than at
the death fails it. Ignoring the refusals (returned, iterated, forwarded) does
not: at each of those points the runtime already shares or copies -- a caller
shares the result it stores, an iterator takes its own view -- so the refusals
are a second line, and nothing in the current compiled output can reach a
returned parameter's holder raw. The one path that could, a by-reference
builtin given a call's result, is also missing its `Only variables should be
passed by reference` notice, so it cannot be a passing case yet.

**P3b as planned, for the record: separate on first write.** For shares the trace cannot bound
-- `$x = $_GET`, a value that escapes to a callee -- a one-way `shared` bit on
`php.Array`: an elided copy sets it on both sides, and every in-place write
path separates (clones) first when it is set. No count, so a second write
after separation may copy once more than the engine would; that costs speed,
never correctness. Then the trace's job inverts: prove **unique** so a write
can skip the check.

**Constraint on P3b.** An array holding a reference must still copy eagerly.
`Clone` flattens a reference only one name holds, *at the time of the copy*; a
lazy clone taken later would capture a later value. `RefHeld` answers it for
one key; the elision needs it for the whole array, kept as a count updated
where a reference is stored into an element.

**Risks.** Every in-place write path in `runtime/php` has to separate --
`Set`, `Append`, `Unset`, the `dim.ref` family, by-reference iteration, and
every by-reference builtin. Missing one is an aliasing bug that prints. P3b
therefore starts with an audit that lists every mutator of `Array`'s buckets,
and a test that fails when a new one appears without a separation.

### P4 -- Reuse an object's storage at its static death (new Foo, 18x)

**The cheap half first, 2026-09-27.** Profiling `new Foo()` in a loop found
costs in front of storage reuse, each fixed at its root:

| cost | fix | `newfoo` |
|---|---|---|
| (baseline, after the array index) | | 9.58 s |
| every `new` filled a per-object Go map of property declarations | the class's table is built once and shared, copy-on-write (`Object.UseDeclarations`) | |
| every `new` dispatched `callCtor`, walking interfaces, for a class with no constructor | skipped when no `__construct` exists in a chain of compiled classes; the destructor is still registered | **5.60 s** |
| `Ctx.destructed` and the emitted `freeObjectSet` were maps that grew with every object that ever died | flags on the object (`destructed`, `idReleased`) | |
| the frame drain list kept every confined object a loop made until the function returned | `appendFrame` drops finished objects before the list grows, order kept | **3.77 s** |

20x -> **7.9x** the engine. Fixture `class_tables_shared.php` (visibility read
from the shared table, a shadowed private, an exception subclass whose engine
properties are declared first, a clone, an inherited constructor -- treating
every class as constructor-less breaks it).

**Storage reuse itself is not done.** What is left in the profile is allocation
and GC scanning of the object and its property table, which reuse would take
away -- but reuse hands a dead object's memory to a new one, so a lifetime
analysis that is wrong once stops being a late destructor and becomes two
objects sharing state. It wants its own attack cases (weak references, a
destructor that stores `$this`, `spl_object_id`) before it is built.

**Trace fact.** Confined, not shared, with a death point the emitter already
acts on (`ReassignReleases`, the frame drain). Plus: the class's destructor, if
any, does not store `$this` (`leaksThis("__destruct")` false) -- a
resurrected object cannot be reused.

**Runtime.** A per-class free list of `*php.Object` with its property table,
reset to the class's defaults on reuse. Handle numbers are already recycled at
the same point (`releaseObjectID`), so what is visible stays the same.

**Expected.** Most of the 40% GC share and the per-object map allocation in
the `new Foo` loop. It does not help an escaping object, which is the majority
in library code (Twig: 667 of 669 sites escape).

**Risks.** Weak references and `WeakMap` keys must be cleared before reuse
(the release path already notifies weak holders; verify it runs first);
`spl_object_hash` of a dead object must not collide observably with a live
one, which it cannot, because the dead one is unreachable.

### P5 -- Input to D3.3, not a speedup

When escaping objects get a real refcount, the trace says **75-79% of them are
only ever moved** (`OWNERSHIP.md`). For those the count never exceeds one, so
retain/release can be elided at the moves. This makes the correct design
cheaper; it is not a speedup over today, which has no refcount at all.

### The array index: a table of our own, not a Go map

**Done, 2026-09-27**, prompted by the user's observation that a Go map inside
the array container did not look right. It was a pre-existing cost the
profiles kept showing -- `build()` spent 34% in `mapassign`, every lookup hashed
a three-field `Key` struct -- and copy-on-write made it worse, because a
separation had to rebuild the map entry by entry. `runtime/php/arrayindex.go`
replaces it with the engine's shape: **packed** while every bucket's key is its
position (no table at all), otherwise an **open-addressed `[]int32` table** of
bucket positions with each bucket's hash cached. Deleted and renamed buckets are
free tombstones, because a lookup compares the bucket's key and flag; a
separation copies two slices. `TestArrayIndexAgreesWithAnOrderedMap` runs
random operations against a plain ordered-map model and fails if the tombstone
check or the packed test is removed.

| | before | after |
|---|---|---|
| `arrpass` return loop | 9.60 s | **7.47 s** |
| `zend_bench` | 7.71 s | **6.96 s** |
| `newfoo` (property tables are arrays) | 10.69 s | **9.58 s** |
| `zend_micro_bench` | 63.33 s | **59.76 s** |

### Properties and integers: the cheap half of what the trace does not buy

**Done, 2026-09-27.** Not a trace fact at all -- the table below lists these as
out of the trace's reach -- but `$this->x++` at 19x was the largest ratio left,
and most of it was questions whose answers the compiler already had.

- **Property fast paths** in the emitted `propGetMode` and `propSet`. Each
  declaration entry carries `Plain` (public, unhooked, not a shadowed private's
  slot) and `PlainWrite` (also untyped, not readonly, one visibility). A read
  of a Plain property that holds a value, or a write to a PlainWrite one that
  holds a value and is not a typed box, skips lazy resolution, mangling, hooks,
  visibility, `__get`/`__set` and the declaration checks: none of them can
  refuse it or run user code for it. `property_fast_path.php` fails if the
  shadowing test, the PlainWrite test, either undef test or the typed-box test
  is removed. Two further guards (`Object.PlainAccess`, the class has no hook
  anywhere; and `Lazy == nil`) are **defensive, not load-bearing**: a mutation
  dropping either still passes, because hooks are recorded per declaration and
  a lazy object's unwritten slots hold undef. They cost a field load each.
- **Integer fast paths** in `Increment`, `Decrement`, `IsSmallerValues` and
  `IsSmallerOrEqualValues`: two ints (and a step short of the range's end) go
  straight to the Go operator. The general comparison asked about enums,
  booleans and objects before reaching the integers.

The fixture also caught two older bugs on the full path, both fixed: reading
an **unset untyped** declared property threw "must not be accessed before
initialization" instead of warning `Undefined property` (and skipped `__get`),
and writing one skipped `__set`.

| | before | after |
|---|---|---|
| `propinc` | 2.85 s (18.7x) | **1.41 s** (9.3x) |
| `newfoo` | 3.89 s | **3.38 s** |

What is left in `propinc` is the place machinery (`propPlace`, `getStep`,
`setVerb`, each copying a large `place` struct), `TakePendingError` after
every step, and the 88-byte `php.Value` itself. Slots by index -- the real
"known object shape" -- would remove the remaining hash lookup; it needs the
class to be final or the property's offset fixed across the hierarchy.

### Call sites that remember (2026-09-28)

The next four largest micro_bench gaps were all the same shape: a generic
helper re-asking, on every execution, questions whose answer is fixed at the
call site. Each literal site now carries a small cache, filled by the first
**successful** access and never by a failure:

- **Property read-modify-write** (`$o->p++`, `$o->p += v`): the read
  (`place.getMod`) finds a plain property's slot in the object's table and
  keeps it on the place; the write (`setMod`) goes through it. The table
  vouches for the slot at the write (`php.Array.SlotSet`: same body, unshared,
  same live key, a plain value), so user code that restructured the object in
  between sends the write down the full path.
- **Static properties** (`C::$p` with a literal class and scope):
  `staticSite` keeps the slot and the declared type. Slots are created once
  and never replaced; visibility is the site's own question.
- **Static calls** (`C::m()`): `staticCallSite` keeps the compiled static
  method and its frame name, once the method resolves as static, visible and
  not an engine special case.
- **Instance calls** (`$o->m()` from a literal scope): `methodSite` is a
  monomorphic inline cache keyed on the receiver's class, for objects with no
  engine state (`Internal == nil`).
- **Class constants** (`C::K`): `constSite` keeps a non-array value that is
  not deprecated (a deprecated constant notices on every read).

`static_property_sites.php` and `call_sites.php` attack each: a site seeing
four receiver classes, a private method from inside and outside, typed
coercion that succeeds then refuses at one site, a reference to a static, a
deferred initializer, an enum case, a deprecated constant read twice, a class
declared conditionally and used before and after. Mutations dropping the
receiver-class check, method visibility, the typed coercion, the deprecation
exclusion or the class guard each fail them. Three guards are **defensive**
-- no mutation of them fails a test: `Internal == nil` (an engine object's
class has no compiled method to cache), `isStatic` at a static-call site (an
instance method is not in the static table), and the array exclusion at a
constant site (every write path separates a shared array, so caching one
would also be safe).

The fixtures found three older wrong answers, now fixed: `C::$p = v` replaced
a reference bound to the static instead of writing through it; and a class
declared conditionally answered `C::$p`, `C::K` and `C::m()` before its
declaration ran (the member tables exist from start-up), where the engine
says `Class "C" not found`. The guard (`requireClass`, autoloading) is
emitted only for classes the program declares conditionally. A fourth is
recorded, not fixed: the engine initializes **all** of a class's deferred
static defaults at the first static access, so one undefined constant in any
default fails every static access; compiled output initializes per property.

| | before | after |
|---|---|---|
| `propinc` | 1.38 s | 0.81 s |
| `staticprop` (new) | 2.03 s | 0.48 s (engine 0.28) |
| `methodcall` (new) | 1.04 s | 0.58 s (engine 0.31) |

`new Foo()` is the remaining large gap and is structural: a confined object
stored in a local is kept on the frame's list until the function returns,
where the engine destroys it at the local's next assignment. A loop of five
million `new` keeps five million dead objects live for the GC, and handle
numbers climb where the engine's alternate. Destroying at the kill point is
the static-destruction work (plan D3.3); `lower.ReleasePrev` is the marker
the renamer already leaves at a promoted store.

### Objects as Go structs, and a smaller Value (2026-09-28)

Two representation changes, both prompted by the property profile after the
call-site caches: `$this->x++` still spent over half its time finding the
property and a quarter copying `Value`s.

**Per-class structs** (the user's choice over a slot vector or fixed bucket
offsets). A compiled class `C` gets `type obj_C struct { php.Object; f
[N]php.Value }`, one field per declared property in the constructor's order;
`Object.Slots` points into it and `Object.Layout` names each slot. `Props`
keeps only dynamic properties. Everything outside the compiled fast paths
reads properties through `Prop*` accessors on `Object`
(`runtime/php/objectslots.go`) that present slots-then-dynamic as the one
ordered table `Props` used to be, so an object in **table mode** -- every
interpreter object, a throwable, anything built outside a compiled
constructor -- behaves exactly as before. A consumer that needs a live
`*Array` (`array_walk` over an object, `ArrayObject` wrapping one, `current()`
on one) calls `Table()`, which **demotes** the object to table mode for good;
fast paths check `Slots` and fall back. The property site cache now keys on
the class's `Layout` pointer and indexes the slot.

**Value is 24 bytes, not 88.** It had a field per kind -- int, float,
string, array, object, reference, resource -- and is copied at every
argument, return, array element and property. It is now a kind, a flags
byte, one word (the int, the float's bits, or the string's length) and one
pointer (string bytes, or the array/object/reference/resource). The fields
were unexported, so only `runtime/php` changed: guarded accessors (`v.s()`,
`v.a()`, ...) answer the zero value for another kind, exactly as reading the
separate field did. A `_ [0]func()` field makes `Value` incomparable, so no
code can compare two strings by where they live; nothing did.
`TestValueIsThreeWords` pins the size.

| | before | structs | + 24-byte Value | engine |
|---|---|---|---|---|
| `zend_micro_bench` | 15.7 s | -- | **12.05 s** | 6.18 s |
| `zend_bench` | 4.97 s | -- | **4.57 s** | 1.21 s |
| `propinc` | 0.68 s | 0.64 s | **0.43 s** | 0.14 s |
| `newfoo` | 3.13 s | -- | **2.39 s** | 0.50 s |
| `arrpass` | 9.78 s | -- | **7.77 s** | 1.38 s |
| `movewrite` | 2.18 s | -- | **1.46 s** | 0.33 s |
| `methodcall` | 0.50 s | -- | **0.38 s** | 0.31 s |

Not yet done: compiled code reaching a proved-plain property by a constant
index into `obj_C` with no site check at all, which needs the closed-world
proof that every subclass keeps the parent's slots as a prefix and none of
them hooks, types or shadows the name.

## What the trace does not buy

Listed so the ceiling is honest. These are most of the remaining 6x on
bench.php and most of the per-operation cost everywhere.

| cost | evidence | the lever |
|---|---|---|
| generic compare and arithmetic | `IsSmallerValues` 22%, `Add` 10%, object-comparison and enum checks on integers | type inference and the specialization table (plan D1/D4); the int-int dynamic check is done (above) |
| properties through a hash | `$this->x++` at 20x, `mapaccess2` and `propSet` | known object shape: slots for a class with no magic and no hooks (the trace's re-entry facts are part of the precondition); the plain-property fast path is done (above), 9.3x |
| list-shaped arrays hash-indexed | 34% of `build()` in `Append` -> `mapassign` | done: packed / open-addressed index (above) |
| iteration | `cursorOf` 23% of `sum()` | the iterator protocol, specialised for arrays |

## Attack cases every elision owes

Differential, expected output from the engine, each built so it fails if the
claimed copy is missing:

- `$b = $a; $b[] = 1;` then read `$a` -- and the same with the write on `$a`.
- A literal assigned in a loop and appended to on one iteration only.
- `$r = &$a[0]; $b = $a; $r = 9;` -- a reference element, before and after the
  copy.
- `next($a); $b = $a; current($b)` -- the internal pointer travels.
- `foreach ($a as $v) { $a[] = $v; }` -- iteration over a snapshot while the
  variable grows.
- A by-reference parameter, a closure `use (&$a)`, a `static` and a `global`
  holding the array.
- A callee that returns `$this->items`, stored and then written by the caller.
- `$s .= $x` where `$s` is a property, a reference and a static.
- A destructor that stores `$this`, with the class allocated in a loop.
- A `WeakMap` keyed by an object that dies at a reassignment.

## Order and the numbers to watch

| phase | quote before and after | must not move |
|---|---|---|
| P0 | the tables above, reproduced | -- |
| P1 | bench.php total and strcat | corpus, fixtures |
| P2 | `build()` isolate; clone share in its profile | corpus, fixtures |
| P3a | issetor, ternary | corpus, fixtures |
| P3b | `$x = $_GET`; a Twig render | corpus, fixtures; the write-path audit test |
| P4 | `new Foo` isolate; GC share | corpus, fixtures, handle numbers |

P1 first: it is the largest ratio, the representation already exists, and the
trace needs only its linear-use extension. P3b and P4 touch the runtime's
invariants and go last, each behind its own attack cases.
