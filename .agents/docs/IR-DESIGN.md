# IR design

The compiler's lower-level IR: what shape it takes and why. Written 2026-08-28
after Phase A's measurements and the [intrinsic catalogue](./INTRINSICS.md),
both of which decide more of this than taste does.

## Types are generic, and covariant in what a container holds

A `TypeMask` says a value may be an array. It cannot say an array **of ints**,
so reading an element answers `mixed` and everything downstream of a container
is untyped -- which is most of a PHP program. `php.Type` carries the mask plus
optional key and element types:

	array<int, int>       array<string, array<int>>       iterator<int, string>

An iterator is an object in the value model -- its cursor lives in one -- so its
mask says object and its parameters say what stepping produces.

### Unknown is the top, not the bottom

A nil parameter means **unknown**, and unknown must behave like "anything". An
array nobody has looked inside may hold a string, so a variant wanting
`array<int>` must not accept it. The natural reading of a nil pointer is
"nothing", and that reading is unsound: it would let the narrow variant fire on
an array of strings and skip a diagnostic PHP emits.

### Covariance, and where it stops

`array<int>` is an `array<numeric>` is an `array<mixed>`. `Signature.Select`
uses `Type.Subsumes`, so a variant declared over `array<numeric>` applies to an
`array<int>` and one declared over `array<int>` does not apply to an array whose
elements nobody has looked at. It composes: `array<array<int>>` is an
`array<array<numeric>>`.

Covariance is sound for a **read** and not for a write. Appending a string to an
`array<int>` produces an `array<int|string>`, so `array.append` computes a
**union** rather than checking containment, and the two directions are kept
apart deliberately.

### The rules that thread it

	array.new()                  array<never>
	array.append(a<T>, v:U)      array<T|U>
	array.set(a<K,V>, k:J, v:U)  array<K|J, V|U>
	dim.get(a<K,V>, k)           V
	iter.new(a<K,V>)             iterator<K,V>
	iter.current(i<K,V>)         V
	iter.key(i<K,V>)             K
	iter.next(i<K,V>)            iterator<K,V>

A folded array literal is typed from its own entries through
`lower.ParseArrayLiteral` -- the renderer's own scanner, so no second spelling
of the format -- and a nested array arrives as another `[...]` and recurses.

### What a container shape buys: a narrower effect contract

Covariance existed for a fortnight before any variant declared a container
shape, so the mechanism was live and unexercised -- a declared capability
nothing performed. Three variants now select on the shape alone:

	iter.new.array(array<K,V>)          NewArrayIterator   pure, no ctx, no error
	dim.get.array(array<K,V>, scalar)   ReadDimArray       warns, cannot throw
	count.array(array<K,V>)             CountArray         pure, no ctx, no error

None of them narrows a *value* representation -- all three still take and
return `php.Value`. What knowing "this **is** an array" buys is the **effect**
half of the signature, and that is where the win is:

- `iter.new` on an unknown subject may be handed a `Traversable`, whose
  `rewind()` is user code that can print and throw. On an array there is no
  rewind to run, so the variant needs no `*Ctx` and returns no error, and the
  emitted Go loses a branch per loop.
- `dim.get` on an unknown subject may be given a non-container and must throw;
  on an array the only diagnostic left is the undefined-key warning, which the
  `silent` argument already carries.
- `count` on an unknown subject may have to call `Countable::count()` -- user
  code again. On an array it is a field read.

The parameters `K` and `V` are unconstrained in all three: these variants ask
only the shape. That is deliberate, and it is why they fire so widely -- a
declared `array` parameter is enough, with nothing known about the elements.

**Reading the census honestly.** Adding a second variant to three intrinsics
moves every one of their call sites *into* the specializable denominator, so
the rate before and after are not the same measurement:

	before   266 of  835  (31.7%)   -- denominator: arithmetic and compare only
	after    681 of 2461  (27.7%)   -- denominator: those plus dim.get/count/iter.new

The numerator is what moved: 266 -> 681 calls now reach a narrower
implementation. The percentage fell while the thing it measures grew, which is
the same trap as `failed` rising as `unsupported` falls.

### Backing: a third axis, for a fact PHP's types cannot hold

`foreach` over an array and `foreach` over a Traversable produce the **same PHP
type** -- an object holding a cursor -- with completely different effect
contracts. Stepping an array is an index into a snapshot; a user Iterator's
`current()`, `key()` and `next()` are method calls that can print, throw and
re-enter. No mask and no element type can tell them apart, and at run time
nothing can either: both cursors are objects of the same unspellable internal
class.

So `Type` carries `Backing`, which records what the compiler proved about the
**subject** rather than anything about the value:

	iter.new(array<K,V>)     iterator[array]<K,V>    -- stepping runs no user code
	iter.new(anything else)  iterator<?, ?>          -- stepping is a method call

`iter.current.array`, `iter.key.array` and `iter.next.array` require it, and each
drops the `*Ctx` and the error return -- three fewer error branches per loop
iteration. The general variants remain for everything else.

Empty `Backing` is the **top**, exactly as a nil element type is: a variant
constrained to array-backed must not accept a cursor nobody has proved anything
about. Getting this backwards makes a foreach over a user Iterator skip the user
code it owes, which is a wrong answer rather than a slow one, so both directions
are pinned and mutation-checked.

It is set only where the subject's mask is **exactly** `array`. `?array` does
not qualify: the null path reaches the general constructor.

### Knowing the key type, not just the container's shape

`array.set` declares `Deprecates | Warns` because `ArrayKey` coerces, and its
only diagnostic is the float-precision deprecation. An `int` or `string` key --
including a numeric string, which becomes an int key silently -- therefore makes
the whole operation pure, and `array.set.key` needs no `*Ctx`. That is the first
variant selected on a **parameter** type rather than a container shape.

### `of` is for a use; `bottom` is for a fixed point

The inference is **optimistic**: it starts values at bottom and widens. That
needs two different answers for "no fact":

- at a **use** site, an absent fact is `mixed`, because a value nobody computed
  may be anything;
- inside the **fixed point**, an absent fact is bottom, because a value still
  being computed must contribute nothing to a join.

Four separate places in the pass needed this distinction and got it wrong the
same way -- the block-parameter join, the place join, the intrinsic result
rules, and the container element rules. Each time the symptom was identical: a
loop-carried type settled on `mixed` on the first round and the union kept it
for ever. `Union` treats the empty mask as its **identity** for the same reason.

## A mode is a different intrinsic, not a flag on one

Two families carried a *mode* alongside the instruction rather than in it, and
both are now separate intrinsics.

### The access mode

Zend's fetch modes -- `R`, `W`, `RW`, `IS`, `UNSET`, `FUNC_ARG` -- arrived as a
`mode=` attribute on the instruction. Most of them had already chosen the callee
at lowering: a `W` on a dimension becomes `dim.ref`, never `dim.get`. What
survived was `IS`, and it was consulted three times in two packages -- by the
emitter to fill a `silent bool` argument, by the emitter again to pick how an
operand renders, and by `ExplicitReads` to choose `read_var.silent`.

A boolean parameter could not tell the truth about it, because **the modes
return different values**:

	$s = "abc";
	$s[9]          // "" and a warning
	$s[9] ?? "d"   // "d" -- out of range is NOT SET under IS

One signature had to declare `Effects: Warns` for a form that does not warn. So
`ReadDim`/`ReadDimSilent`, `PropGet`/`PropGetSilent` and `CopyValue`/
`CopyValueSilent` are pairs, each with its own effect contract, and the mode is
consulted exactly once -- in `IntrinsicCalls`, where the opcode becomes a call.
It is dropped from the instruction there, because a spent fact left lying in the
IR is how it came to be scanned for three times.

The silent forms are not free of effects: a float key still deprecates under
`??`, which the engine settled and a reading of `ArrayKey` would not have.

### The write mode

`$a[$k] = $v` was a place-producing fetch followed by `store`, and `store`
dispatched on the place's **shape at run time** -- `p.direct`, then
`p.slot != nil`, then `p.recv` -- to decide whether it was writing a slot, a
dimension or a property. The lowering already knew: it had just emitted
`dim.ref` rather than `prop.ref`.

Writes are now `op(container, key, value)`, symmetric with the reads:

	dim.get(c, k)            dim.set(c, k, v)
	dim.get.silent(c, k)     dim.append(c, v)
	prop.get(o, n)           prop.set(o, n, v)

`WriteDim` had **no variants at all** before this -- writes had never entered
the intrinsic layer, so nothing about a write was described anywhere. The
`appending bool` inside `php.WriteDim` was the same defect as `silent`, and
`AppendDim` is now its own intrinsic: no key, the index chosen at write time,
and appending to a string is its own error where writing an offset is legal.

A static property joins them: `C::$p = v` is `sprop.set(name, class, value)`.
Its class operand is a **reference**, not a value -- a literal name, or
`self`/`parent`/`static` resolved against the emitting scope -- so it has its own
argument shape, and access to an undeclared static property is the only effect
left. There is no `__set` to re-enter, no hook, and nothing to autovivify.

What remains a place is what a place is actually for: the **intermediate** fetch
in `$a["x"]["y"] = 1`, which autovivifies and is a real operation, plus
by-reference binding, `unset`, and the read half of a compound assignment --
`C::$p += 1` still fetches a place, because it reads before it writes.

The read side of the static-property family is still an opcode (`sprop.get`),
and it carries the last `mode=` in the IR, which nothing reads.

## Declare the fact where it cannot be re-derived wrongly

The rule this design keeps arriving at from different directions, found jointly
with the runtime session on 2026-08-29/30 after it had settled three unrelated
questions the same way:

| question | rejected | accepted |
|---|---|---|
| does a suspension reach the caller? | infer at each call site | `.suspends_to_caller` **on the function** |
| does this read warn on undef? | a per-opcode table the emitter consults | `mode=IS` **on the operand** |
| what are diagnostic paths relative to? | discover a root by walking up for `.git` | the root **declared**, and its policy recorded in the artifact |

Each rejected alternative is *a rule someone has to apply correctly every time*,
and each accepted one moves the fact to where a consumer simply reads it. The
difference shows up as a class of bug rather than as style: a rule fails silently
at the one call site nobody thought about, and the failure is invisible until a
consumer needs exactly that answer — which is the same time-axis property as
[a latent defect in the IR is invisible until a consumer needs the part that is
wrong](./TESTING.md).

Two consequences worth stating, because they are not obvious from the rule:

- **Declared beats discovered even when discovery would usually be right.**
  Go's `-trimpath` does not search for a module root; it uses the path declared
  in `go.mod`, which is why two checkouts of one module in different directories
  produce identical binaries. A heuristic makes the output depend on filesystem
  context outside the compiled source.
- **Where nothing is declared, pick a deterministic function of the input set,
  and prefer the one that is stable under adding inputs.** PHP has no dependable
  project identifier, so the compilation root is the entry file's directory
  rather than the longest common prefix of all inputs: the latter is equally
  deterministic but shifts every reported path when one input outside the tree
  is added.

The enforcement-side twin of this rule is in
[QUALITY_GATE.md](./QUALITY_GATE.md): a rule is remembered, a gate is not
optional. Declaring the fact removes the judgement; gating the output removes
the chance to forget it.

**Status: design settled; a prototype lowering in `.agents-workspace/tmp/lower/`
exercises all of it and covers 95.9% of the corpus, with every remaining bail naming an unimplemented opcode.** Every decision below is
argued from a measurement or from a contract in `INTRINSICS.md`; where it is
argued from neither, it says so.

## The decisions, up front

| question | answer |
|---|---|
| stack or register | **register**, SSA, virtual registers |
| instruction form | **variadic operand list**, not strictly three-address |
| sea-of-nodes | **no** — actively wrong here |
| merges | **block arguments**, not φ nodes |
| exceptions | **region-based**, not `invoke`-style edges |
| `finally` | **inlined at each exit**, not a subroutine — costs 0.70% of the corpus |
| generators | `yield` is a terminator; `GENERATOR_CREATE` splits the eager prologue from the resumable body |
| declarations | definition is module-level, declaration is an instruction where the engine put it |
| ordering | **total within a block**, relaxed only where an intrinsic is proven pure |
| places | first-class operand kind, produced and consumed but never stored |
| values crossing blocks | **start as places**; mem2reg promotes later |

---

## Register, not stack

The input is already register-shaped and already has the stack we would be
adding back. A `zend_op` is `op1`, `op2`, `result` over a slot space — three
addresses, not a stack — and the one stack-like structure in the op_array is the
`INIT_FCALL` / `SEND_*` / `DO_FCALL` protocol, which the un-flattening pass
exists to **remove**. Lowering to a stack machine would mean rebuilding, in our
own IR, the thing we just spent a pass eliminating.

The usual argument for a stack IR is compactness and no register allocation.
Neither applies: **we emit Go source, not machine code.** Go's compiler does the
register allocation, and nobody counts the bytes of a `.go` file. What we need
instead is explicit def-use — for the type inference of Phase D1, the
representation narrowing of D2, and the escape analysis of D3 — and a stack
makes def-use implicit in stack position, which every one of those passes would
have to reconstruct first.

Phase A3 is consistent with either: 90.6% of temporaries have exactly one use,
which a stack expresses as push/pop and SSA expresses as a def with one use. It
does not decide the question; the three analyses do.

## Not sea-of-nodes — the ordering is the semantics

`ext/opcache/jit/ir/` is a sea-of-nodes IR, and since we are otherwise following
the architecture PHP's own JIT arrived at, the temptation is to follow it here
too. **Don't.**

A sea-of-nodes IR earns its keep by letting pure nodes float free of order, so a
scheduler can place them optimally. That freedom is exactly what we cannot use:

> Of the intrinsic rows in `INTRINSICS.md`, **44 declare an effect** — a
> diagnostic, a throw, output, or re-entry into user code — against **16 that
> are pure**.

Roughly three quarters of the alphabet is order-sensitive, and PHP's observable
behaviour includes diagnostic ordering at instruction granularity. The
arithmetic contract is the sharpest case: `2.5 % ""` must deprecate *then* throw,
and `"" % "2abc"` must emit only the throw, because the walk stops at the first
operand that cannot convert. `php2phpt` grades byte-exact stdout.

So a sea-of-nodes IR would spend its complexity budget buying a freedom that a
quarter of our instructions can use, while discarding the total order that the
other three quarters *are defined by*. And the scheduling it would do for us is
work Go's compiler does anyway. It is the wrong tool for this program.

The design rule that follows:

> **Instructions are totally ordered within a basic block. Reordering or
> eliminating one is legal only where `INTRINSICS.md` proves it pure at the
> operand types inference established.**

Effects therefore travel on the instruction, not in a side table — an
optimization pass must not have to look anywhere else to know whether it may
move something.

## Values

An SSA value has a **representation type**, from the closed set:

```
i64  f64  bool                                   -- unboxed scalars
*String  *Array  *Object  *Resource  *Ref        -- handles
zval                                             -- the boxed php.Value
place                                            -- an lvalue; see below
```

PHP types (`int|string|array|…`) are **not** in this lattice. They are an
analysis fact attached to values by D1, and their only job is to choose which
specialization may fire. Keeping them out of the IR's type system is what lets
the emitter be a direct type-for-type mapping with no PHP knowledge in it.

Lowering emits everything at `zval` and that output must be correct on its own.
D2 narrows to a handle type where the kind is known; D4 narrows to a scalar
where the type is known. **Both are optional** — which is what lets the untyped
emitter ship before either exists.

### Places are values, but only some instructions accept them

`INTRINSICS.md` establishes that a write path is not a value: a `FETCH_DIM_W`
result is a pointer into a container, and Zend needs a whole
`zend_ssa_var_info.indirect_reference` bit to say so. Rather than inherit that,
places get their own representation type:

```
%1 = place.local  $a
%2 = place.dim    %1, %k, mode=W
      store       %2, %v
```

`place.*` instructions produce them; `load`, `store`, `bind_ref`, `unset` and
`fetch_for_write` consume them. Nothing else may. A place is never stored into a
variable, never a block argument, never a call operand — so "a temporary holding
an indirection", the thing that makes Zend's SSA awkward, is **ill-typed in this
IR by construction**. That is the single biggest structural gain over the input.

`mode` is part of the `place.dim` / `place.prop` instruction, not a flag on the
access, because it decides semantics: `R` warns on a missing key, `W`
autovivifies, `IS` is silent. Its five-way write behaviour — including
`false` deprecating where `true` is fatal — is in `INTRINSICS.md`.

## Instructions are variadic

Three-address form cannot hold a call. Once the un-flattening pass has folded
`INIT` / `SEND` × N / `DO` into one node, that node has N operands, and the same
goes for array construction (`INIT_ARRAY` + `ADD_ARRAY_ELEMENT` × N) and rope
concatenation. So an instruction is:

```
%result = opcode [attributes] %operand, %operand, ...
```

with a variable-length operand list, as LLVM has. "Three-address" describes the
*arithmetic*, not the form.

Call attributes carry what the send modes encoded: per-argument by-value,
by-reference, prefer-ref, unpack, or a parameter name. `ZEND_SEND_PREFER_REF` is
collapsed to a bool in the harvest today and must be recovered before calls can
be lowered faithfully.

## Append is not one operation, and the split is a shape change

Appending is four instructions in the IR, not one, and they do not share a
shape. Counted over CakePHP:

| form | sites | shape |
|---|---|---|
| `%n = concat %a, %b` | 2,273 | **defines a value** — a rope under construction |
| `%n = array.append %acc, %v` | 967 | **defines a value** — an array literal |
| `%n:place = dim.append %c` + `store` | 563 | **writes a place** — `$a[] = v` |
| `write_var $s, %v` after a `concat` call | 287 | **writes a place** — `$s .= $x` |

The pure/mutating choice is not a property of the syntax. It is a property of
the destination's **ownership and representation**:

```
strbuf ← (str | strbuf)     mutating; the destination is uniquely owned
str    + (str | strbuf)     pure; produces a new string
```

Four operand combinations, two result shapes. The same duality holds for arrays:
`array.append` on an `*Array!` mutates in place, on an `*Array&` it must clone
first.

### Why this cannot be a `.<spec>` suffix

`IR-ASSEMBLY.md` reserves `.<spec>` for Phase D4 — `add.i64`, `concat.str` —
where specialization picks a cheaper *implementation of the same instruction*.
A specialized `add.i64` still defines a value.

**The builder transform does not preserve shape.** Turning `%n = concat %a, %b`
into an in-place append to `%a` removes the definition of `%n` and turns `%a`
from an operand into a destination. Every consumer of `%n` has to be rewritten
to read `%a` instead, and `%a` must be dead afterwards. That is a rewrite D3
performs on the instruction graph, not a name D4 chooses — so it does not belong
under the suffix, and the specialization table cannot express it.

### Two sources of builders, and only one needs inference

Worth separating, because they carry very different proof obligations:

- **Compiler-invented accumulators** — the rope chain (`ROPE_INIT`/`ADD`/`END`)
  and the array literal (`INIT_ARRAY`/`ADD_ARRAY_ELEMENT`). Uniqueness here is
  not inferred, it is **in the encoding**: `ROPE_ADD`'s result slot *is* its own
  op1, an in-place append, and the slot is a TMP the compiler invented for one
  literal that no PHP expression can name — so no `&` can reach it.
- **Program variables** — `$s .= $x` in a loop, the case that is asymptotically
  wrong today. Aliasing, closure capture and references to typed properties are
  all live questions, and every uniqueness claim owes a differential case built
  to break it.

The first group is lowered as builders directly (2026-08-29):

```
%4:*strbuf = strbuf.new "x"
%5:*strbuf = strbuf.append %4, $a     ; consumes %4 — same storage
%9:*str    = strbuf.finish %8
```

`strbuf.append` **consumes its buffer operand**: the definition and the operand
are the same storage, which is what lets the emitter reuse one Go builder rather
than allocate per piece. The SSA shape is preserved — every value still has one
definition — so nothing downstream needs to understand mutation.

Measured when this landed: **1,919 buffer definitions across CakePHP and
`Zend/tests`, every one consumed by a builder op, zero escapes.** The structural
claim is checked, not asserted.

### A dynamic variable is its own opcode, not a local with a computed name

`ZEND_FETCH_R`/`_W`/`_RW`/`_UNSET`/`_FUNC_ARG` on a *local* is always a variable
variable. Verified against the engine: `$$name = 1` compiles to `FETCH_W` with
the name in op1, while `${"lit"}` is folded to an ordinary CV at compile time and
never reaches the opcode at all.

These lower to `dynvar.get` / `dynvar.ref`, not to the read and write
intrinsics.
The distinction is load-bearing rather than cosmetic: rendered as
`read_var $name`, a variable variable is **indistinguishable from reading a
variable actually called `$name`**, and any analysis that must know whether a
frame can reach its own locals without naming them is silently defeated. That
happened — the loop-builder analysis promoted a variable in a frame using `$$x`,
and only an attack case caught it, because no function in either corpus combines
the two.

### What this does *not* decide: convergence

Making the builder explicit says it *is* a builder. It does not say what it
should become. Three questions remain, and none is local:

- **Where `strbuf.finish` belongs.** It currently sits exactly where `ROPE_END`
  was, which is the conservative always-correct point. If the result flows only
  into another append, an `echo`, or a callee that merely reads it, the finish
  could sink or disappear — which needs to know what the consumer does with it,
  and that is the **call graph plus escape analysis**.
- **Whether a buffer is worth it at all.** This is a **cost** question, and the
  corpus says it is the common case rather than a tail: **53% of CakePHP rope
  chains and 72% of `Zend/tests` ones are three pieces**, where the builder is
  four instructions and an allocation against two `concat`s. 99% are ≤7 pieces.
- **Whether a buffer can be carried across loop iterations**, which is the case
  that actually fixes the O(n²), and which belongs to the program-variable half.

So the split is: the structural half establishes *that this is a builder and the
buffer is unique*; convergence — where it materialises, or whether it should
exist — is D3/D4 with a cost model. Doing the structural half first is what makes
that later work a rewrite over named instructions instead of a rediscovery
problem: with pure `concat` chains, D3 would first have to work out that seven
concats were one interpolation.

## Control flow: basic blocks with block arguments

A function is a CFG of basic blocks. Each block takes a parameter list; each
terminator supplies arguments to its successors. **No φ nodes.**

```
bb2(%x: zval):
   ...
   br %c, bb3(%x), bb4(%y)
```

Block arguments are equivalent to φ in expressiveness and better in two ways
that matter here. First, a φ is notionally "at the top of the block but
evaluated on the edge", which every pass has to special-case; a block argument
just is on the edge. Second — and this is the one that decides it — **it gives
generators a natural shape**, below.

**How much they matter depends on how far the rest of the lowering gets, and an
earlier measurement here understated it.** A prototype with *no* cross-block
value handling — per-block SSA, bail otherwise — blocked only 60 bodies (0.6%)
when it covered 59.4% of the corpus. At 92.3% coverage the same limitation
blocks **246 bodies (2.3%)**, and is the largest remaining *category*. The first
figure was an artefact: bodies that stopped early on a missing opcode never got
far enough to need a cross-block value.

**And the fix turned out to be this document's own rule, not block arguments.** A
value that outlives its block is a *place*; applying that to temporaries as well
as to CVs and the `foreach` iterator took the count from 246 to **62** and
coverage to 94.0%. It is a pre-pass that finds slots read in a block that did not
define them, and mirrors each definition into a named local — no liveness
analysis, no edge rewriting.

Block arguments remain the design where they are the natural shape: a `yield`
resumption and a caught exception, both of which the prototype emits. For
ordinary temporaries the place rule is enough.

The rule that does the work instead is: **a value that outlives its block starts
as a place, and mem2reg promotes it later.** That began as "CVs stay as places",
since the op_array's CV/TMP split maps exactly onto LLVM's alloca/register
split; the `foreach` iterator then forced the same treatment, being defined by
`FE_RESET` in one block, read by `FE_FETCH` in another and ended by `FE_FREE` in
a third. The interpreter keeps it in a side map keyed by the temp slot for the
same reason.

Terminators: `jmp`, `br`, `switch`, `ret`, `throw`, `yield`.

Reducible control flow becomes structured Go directly, which is what we want:
Go's own optimizer works on loops it can see, and `goto` cannot jump into a
block anyway.

Irreducible flow falls back to a `for { switch state { … } }` dispatch over the
block index — correct, but it throws away most of the benefit, so it must be
counted and reported rather than applied silently.

**Measured 2026-08-28: zero of 10,738 op_arrays in `Zend/tests` are
irreducible.** PHP's `goto` cannot jump into a loop or switch, which makes a
multi-entry cycle hard to construct — though not impossible, and the detector
was validated on one that the engine accepts and runs before the zero was
believed. So structured Go is the normal path and the dispatch fallback is
insurance, not a design constraint. Keep counting it: a corpus outside
`Zend/tests` could differ, and the fallback existing is what makes that safe.

Two thirds of op_arrays are a **single basic block** and 92.5% are four or
fewer, so the emitter should special-case the straight-line function.

The 147 op_arrays (1.37%) containing `finally` were excluded from that claim:
`FAST_RET` returns to the address `FAST_CALL` pushed, which is not a static
edge, so their CFG is incomplete — as it is in Zend's own `zend_cfg.c`.
**Inlining finally bodies removes the exclusion** (see below): with no
`FAST_RET`, every CFG is fully static. Re-measured that way, the result is still
**0 irreducible, now across all 10,738** rather than 10,591.

Detail in `.agents-workspace/tmp/CFG-findings.md`.

## Exceptions are region-based

Two designs were considered.

**LLVM-style `invoke`**, where every instruction that can throw is a terminator
with a normal and an exceptional successor. Precise, and standard. But 44 of the
intrinsic rows can throw, so nearly every instruction would terminate its block
— a CFG of one instruction per block. That is not an IR, it is a linked list
with extra steps.

**Region-based**, where a block belongs to a protected region and throwing is an
implicit edge to that region's handler. This is what the harvest already gives
us — `TryCatch{try_op, catch_op, finally_op, finally_end}` — so it costs nothing
to carry across, and it keeps blocks block-sized.

Region-based it is. The cost is that the CFG is not the whole story: a pass that
reasons about reachability must consult the region table too, and a pass that
moves an instruction across a region boundary changes which handler catches it.
Both are real and both are manageable; the `invoke` alternative is not.

`finally` needs care beyond this. `INTRINSICS.md` records that it is a
subroutine, not a `defer`: its own `return` replaces the try's, and it can
**swallow an in-flight exception**. So `finally_call` / `finally_ret` stay
explicit in the IR rather than being lowered to Go's `defer`, which cannot
express either.

## `finally` is inlined, not called

An earlier draft kept `finally_call` / `finally_ret` as explicit instructions,
mirroring Zend's subroutine model. **Measurement says inline it instead**, and
duplicate the body at each exit — which is what javac does, and why the JVM
deprecated `jsr`/`ret`.

The engine's shape, harvested from `try { if ($c) return 1; echo "t"; } finally
{ echo "F"; } return 2;`:

```
  2  ZEND_FAST_CALL  op1=7  -> TMP:1      ; call site 1, saves a return address
  3  ZEND_RETURN     CONST:0
  5  ZEND_FAST_CALL  op1=7  -> TMP:1      ; call site 2
  6  ZEND_JMP        9
  7  ZEND_ECHO       "F"                  ; the shared body
  8  ZEND_FAST_RET   TMP:1                ; indirect return
```

Two call sites, one body, and an indirect jump. Inlining replaces each
`FAST_CALL` with a copy of the body followed by that site's continuation, plus
**one more copy for the exceptional path** — which is not an explicit
`FAST_CALL` at all, but is driven by the region table and ends by rethrowing.
Each copy is specialised by what it does at the end: return, fall through, or
rethrow.

The cost, measured over the corpus:

| | |
|---|---|
| `finally` regions | 194 |
| oplines added by inlining | **907 (0.70% of the corpus)** |
| worst single case | 4 call sites, 50 oplines |
| finallys with 1 explicit site | 132 of 194 (68%) |
| finally bodies of ≤5 oplines | 166 of 194 (86%) |

Essentially free — and implementing it took the prototype lowering from 94.0% to
**95.1%**. It also fits a linear lowering better than block duplication would:
hitting `FAST_CALL` splices the finally body's oplines into the front of the work
queue, and `FAST_RET` disappears because the continuation is already next. The
body's own blocks are never emitted standalone.

It buys two more things beyond simplicity:

- **Every CFG becomes fully static.** `FAST_RET` is an indirect jump, which is
  why the reducibility measurement had to exclude 147 op_arrays and why Zend's
  own `zend_cfg.c` accepts the same imprecision. Inlining removes it, so that
  caveat disappears.
- **`finally`'s hard semantics fall out.** Its own `return` replacing the try's
  is just a `ret` in that copy. Swallowing an in-flight exception is the
  exceptional copy ending in `ret` instead of rethrow. Neither needs special
  machinery — which is the real argument, because Go's `defer` can express
  neither.

### The frame of a resumable function

A function that suspends is entered many times, so what outlives a suspension
cannot be a Go local. That set is exactly its **places** — no SSA value is used
outside its defining block, and a suspension is a block terminator, so nothing
live at one is a value. The frame needs no liveness computation; the invariant
supplies it, and `php2lower -verify` checks the invariant.

```
.frame [ $rows: zval, $seen: zval, $__iter5: zval, $r: zval, $__t6: zval ]
```

Two rules the declaration encodes:

- **A slot's type is the join of every write to it**, not the first. `$x = new
  C; yield; $x = 5;` is one slot holding either, so it is a `zval`; declaring it
  `*obj` would have the emitter store an integer into an object field. In the
  other direction a string builder is `*strbuf` and must not be boxed. A foreach
iterator used to be `*iter` for the same reason; its cursor now lives in an
internal object, so the handle is an ordinary `zval`.
- **Only compiler slots may be narrowed by liveness.** The program's own
  variables must live as long as the frame does: PHP keeps a generator's locals
  alive until the generator dies and orders their destructors by that, so
  dropping one after its last read moves observable output. `FrameSlot.Compiler`
  marks the ones without that obligation — about half the slots in practice.

### Suspension is the compiler's business, not the runtime's

`yield` is a terminator. `Fiber::suspend()` is one too, even though the engine
gives it no opcode of its own and compiles it as a static call.

That recognition is deliberate. **If suspension stays an opaque call, the only
thing that can implement it is a suspension-aware runtime** — real coroutines
underneath, goroutines in practice. Made explicit, the suspension point is a
control-flow split the compiler can see, the continuation is a block, and a CPS
rewrite has something to reify:

```
fiber.suspend(byval $v) -> bb1
bb1(%1: zval):                    ; what resume() passed, on the edge
```

Recognition is by literal and is unambiguous: a class name inside a namespace
does **not** fall back to the global one (the opposite of the function rule), so
the compiler has already resolved `use Fiber`, and `Fiber` is final and internal
so nothing else can present that name. The completing `DO_FCALL` is found with
the pending-call stack, since the suspension is the call's completion rather than
its `INIT`.

The driver side — `start`, `resume`, `throw`, `getReturn`, the state predicates,
and `new Fiber` — are method calls on a receiver whose class the lowering cannot
know, so they stay calls until Phase D supplies it.

### The exceptional exit gets its own copy

`finally` is inlined at each exit, and there are two kinds. The normal exits are
the `FAST_CALL` sites. The exceptional one is the region itself: an exception
leaving the protected range runs the finally and then keeps unwinding.

So each region with a finally carries one extra copy of the body ending in
**`rethrow`**. That terminator is the whole difference between the two exits, and
it is what `ZEND_FAST_RET`'s polymorphism encodes in the engine — return to the
call site on the normal path, resume unwinding on this one. The copy's region is
the *enclosing* one: an exception raised inside a finally propagates outward, not
back into the try it belongs to.

Without it the region pointed at a `Dead` block — the body's own blocks, which
exist only as copies — so the CFG's exception edge led somewhere no pass would
look.

The body is **duplicated as real blocks** with its jump targets remapped, once
per exit. An earlier version spliced the body's *oplines* into the block that
reached it, which cannot represent a body containing control flow — the
terminator gets set part way and instructions keep being appended after it, so
an `if` inside a `finally` ran both arms.

### This is inlining a continuation, and it has a boundary

The finally body is a continuation shared by every exit. Duplicating it is what
you do when that continuation is statically known at each site, and `FAST_RET`'s
polymorphism — return here, or resume unwinding — is the two continuations being
distinguished.

`yield` inside a finally copies soundly: each copy is a distinct program point
and resumes into its own next block, which is also what the engine has after its
own inlining. `Fiber::suspend` needs nothing at all, having no opcode.

What does **not** work is a `yield` as the body's last statement. Its resume
block is the copy's continuation, which is shared between copies, so the resume
value would arrive at one block from several suspensions with different
numbering. The continuation can no longer be inlined; it has to be **reified as
a value**. That is the line past which generators and fibers want a CPS rewrite,
with unwinding expressed as invoking the exceptional continuation rather than as
a `rethrow` terminator plus a region table.

## Generators: `yield` is a terminator

A `yield` suspends the frame, and where it resumes it receives the value that
`send()` supplied. With block arguments that is exactly an edge:

```
   yield %k, %v -> bb7(%sent: zval)
bb7(%sent: zval):
   ...
```

The resumption value arrives as a block argument, which is why block arguments
beat φ nodes for this IR specifically. The backend then chooses: a goroutine
with channel handoff — which `runtime/interp/fiber.go` already proves works — or
a state-machine transform where each `yield` edge becomes a state. **The IR does
not commit to either**, which is the point of making it a terminator rather than
a call.

The harvest confirms the shape exactly. `function g($n) { $x = yield 1;
yield from [2,3]; return $x; }` gives:

```
  0  ZEND_RECV              -> CV:0        ; the eager prologue
  1  ZEND_GENERATOR_CREATE                 ; the split point
  2  ZEND_YIELD      CONST  -> VAR:2       ; result IS the send() value
  4  ZEND_YIELD_FROM CONST  -> TMP:4       ; result is the delegate's return
  6  ZEND_GENERATOR_RETURN  CV:1
```

`YIELD`'s *result operand* is what `send()` supplied — so the block-argument
edge is not an encoding choice, it is what the opcode already means.

### `GENERATOR_CREATE` is a split point, not a flag

It sits **after** the `RECV` instructions, and that position is observable:

```php
function g(int $x) { yield 1; }
echo "before\n"; $it = g("bad"); echo "after-call\n";
```

prints `before` and then throws — the `TypeError` fires **at call time, before
the generator object exists**, not at first resume.

So a generator function splits into two pieces: an **eager prologue** (parameter
binding and type verification, run when the function is called) and a
**resumable body** (everything after `GENERATOR_CREATE`). Modelling
`GENERATOR_CREATE` as a mere function flag would put the argument check in the
wrong place. It is a terminator ending the prologue block.

The hard part is unchanged: a `finally` crossing a yield must still run when an
abandoned generator is closed. Inlining finallys (above) helps here — each yield
edge's copy is already specialised — but the close protocol still has to drive
them.

## Declarations: the definition is module-level, the declaration is an instruction

`DECLARE_CLASS`, `DECLARE_LAMBDA_FUNCTION`, `DECLARE_FUNCTION` and
`DECLARE_ANON_CLASS` are not ordinary instructions — their operand is a whole
nested function or class. They were the largest single group the prototype could
not lower (1,636 bodies), and implementing them took coverage from 59.4% to
**69.6%**.

The split that works:

| | |
|---|---|
| **definition** | a module-level entity with a symbol — `@h`, `@A` |
| **declaration** | an instruction, at the position the engine put it |

```
%0:*obj   = closure @{closure:file.php:3}      ; value-producing
%1:*class = declare_anon_class "class@anonymous\x00file.php:4$0"
            declare_class "b"                  ; effect only
            declare_function @h                ; effect only
```

**Declarations stay as instructions rather than being hoisted, because their
position is observable.** `traits/constant_005` prints before a class
declaration and expects the fatal after it (`TODO.md`), and a declaration inside
an `if` runs only when control reaches it — the prototype emits exactly that,
with `declare_class` and `declare_function` landing in the guarded block.

Three things learned from the harvest that a lowering will otherwise get wrong:

- **An unconditionally declared top-level class gets no declaration instruction
  at all.** It is early-bound at compile time and simply exists. Only the
  conditional case emits `DECLARE_CLASS`, so "declared" is a per-entity property
  and not merely the presence of an instruction.
- **`op2` is typed `IS_UNUSED` and carries an index into `OpArray.closures`.**
  Another operand whose payload the type field denies. Verified: a body with two
  closures and one conditionally-declared function yields indices 0, 1, 2 against
  exactly those three entries in order — so `closures` holds conditionally
  declared *named* functions too, despite its name.
- **The operand is a lowercased lookup key, not a display name.**
  `class MixedCase {}` produces `literals[1] = "mixedcase"`, and separately
  `literals[2] = "\x00mixedcase/path/to/file.php:2$0"` — a mangled compile-time
  key **beginning with a NUL byte**. That is concrete evidence for the
  outstanding `Literal.string_value` → `bytes` change: these names cannot survive
  a format that assumes text.

## What the IR does *not* model

Deliberately, because the input does not contain them:

- **`print`.** The engine already lowers it to `ZEND_ECHO` plus a constant `1`;
  there is no `ZEND_PRINT` opcode in this build. Anything the compiler has
  already lowered is not ours to model, and an intrinsic for it would be
  unreachable.
- **Named vs positional arguments** as distinct forms — one call node, with a
  name attribute on the operands that carry one.
- **`elseif`, `for`, `while`** and every other surface construct. The op_array
  has already reduced them to branches.

## Open questions

1. **Does the IR keep a literal pool, or inline constants as instructions?**
   Leaning inline (`const.i64 42`), since the pool is a compactness measure and
   we are not compact. But a folded array literal is large and may want pooling.
2. **How are `static` locals represented?** They are places whose home is a
   per-`Ctx` table keyed by `(op_array, name, scope)` — but `Ctx` has nowhere to
   put that today, so the IR's answer depends on a Phase B API addition.
3. **Is `place` one type or several?** One keeps the value space uniform; several
   (`place.local` vs `place.dim`) would let the type system reject, say,
   `bind_ref` on an append place. Undecided.
4. **How much of the region table survives into emission?** Go's `recover` is
   per-function, not per-region, so the emitter has to reconstruct region
   membership at run time — probably as a state variable. Not designed yet.

## How big the lowering is

Measured 2026-08-28 (`.agents-workspace/tmp/OPCODE-COVERAGE.md`). 194 distinct
opcodes occur across the corpus, and **coverage must be counted in bodies, not
oplines**, because one unhandled opline bails a whole function:

| top N opcodes | of oplines | of bodies |
|---|---|---|
| 40 | 87.7% | **59.3%** |
| 100 | 97.7% | **88.5%** |

So Phase C is "most of the opcode set", not "the head of the distribution" —
the obvious plan of implementing the top 40 lands at 59% of functions, not 88%.
The tail is cheap per item, though: 39 opcodes occur fewer than 20 times each.

The call protocol is **43.9% of all oplines** (`INIT_*` 13.9%, `DO_*` 17.5%,
`SEND_*` 12.6%) describing about 18,060 calls, so un-flattening it collapses
roughly 39,000 instructions. With the other node groups folded in, the IR should
come out around **a third smaller than the opline stream** — which is the
concrete payoff of un-flattening, and the reason to build the call node first.

## The pipeline has been run end to end

Small, but no longer hypothetical. `.agents-workspace/tmp/goemit/` emits Go from
the harvested IR for a narrow subset — literals, locals, `echo`, `+ - *`,
concatenation, the comparisons, branches — and `thirdarm.sh` builds and runs it
against the engine:

```
20 passed, 0 failed, 0 refused
```

Including the cases where naive Go would answer differently — `PHP_INT_MAX + 1`
promoting to `9.2233720368548E+18` rather than wrapping, `"10" + 5` giving 15,
`0 < "abc"` being true, `"0" == null` being false, assignment copying — and,
more importantly, **all three rows of the diagnostic ordering table**:
`2.5 % ""` deprecating then throwing, `2.5 % "2abc"` deprecating then warning,
and `"" % "2abc"` throwing *only*, with no warning for the operand the walk
never reached.

That last group is the one worth having. It works because two of this
document's claims are emitted as code rather than asserted: `.line` directives
become `ctx.SetLocation(file, line)` calls, and the diagnostic half
(`php.AcceptsArithmetic`) is emitted before the value half (`php.Mod`) — which
is the split `runtime/php` was written with, the compiler being the caller that
"has the Ctx needed to raise it".

User function calls work too — `INIT`/`SEND`×N/`DO` becomes one Go call
expression — including recursion, mutual recursion across a forward reference,
by-value argument copying, and `fact(21)` overflowing int64 to float mid-recursion.

**The artefact is standalone, confirmed not assumed**: it builds with
`CGO_ENABLED=0` and `ldd` reports "not a dynamic executable". No PHP engine, no
libc. `runtime/php` being pure Go is what makes that possible.

Two Go rules the emitter had to learn, both worth knowing before Phase E:
**Go rejects a label nothing jumps to**, so only real branch targets get one;
and every declaration must be hoisted to the top of the function, because Go
forbids `goto` jumping over a declaration — which also removes any need for
dominance analysis to place them.

Detail and limits in `.agents-workspace/tmp/THIRD-ARM.md`.

**Pushing it into arrays found the boundary of `runtime/php`, and put a number
on Phase B.** `runtime/php` is the data structure — `Array.Get`/`Set`/`Append`
and the operators — while every rule about what a dimension *access means* lives
unexported in `runtime/interp`. An emitter can build a PHP array and cannot read
one faithfully.

Reproducing those rules in the emitter cost **47 of 117 lines (40%) of the
emitted file**, and they were silently wrong: `$a[1.5]` must deprecate and use
the integer key `1`, and the copy used the string key `"1.5"` and reported an
undefined key. Twenty-two passing cases had not touched it. That is
`QUALITY_GATE.md`'s "gap masquerading as working behaviour", arriving on its own
rather than by choice — which is the argument for the extraction rather than for
working around it.

The requirements list this produced is in
`.agents-workspace/tmp/RUNTIME-RT-REQUIREMENTS.md`.

## What this rules in and out

It rules **in** the untyped emitter shipping first: lowering at `zval` with
region-based EH and a `for { switch }` fallback is a complete, correct back end
that needs no inference at all. That is the milestone that proves the standalone
artifact.

It rules **out** treating the IR as a place to be clever. The ordering discipline
means most classical optimizations are illegal here without a proof from the
intrinsic table, and the table is the only thing that can supply one. The IR's
job is to make the un-flattening explicit and the def-use edges visible; the
cleverness lives in Phase D, gated on `INTRINSICS.md`.

## Three layers, and the test for which one a function belongs to

Corrected 2026-09-02, after `INTRINSICS.md` spent months calling the IR's
opcodes "intrinsics". They are different layers, and conflating them let an
unowned intrinsic layer grow inside the generated program.

| layer | scope | lives in |
|---|---|---|
| **IR opcode** | the compiler's instruction set | `internal/lower` |
| **Intrinsic** | essential builtin functions that drive the runtime | `runtime/php` |
| **Library** | ordinary PHP functions, closed and inherited | `runtime/php` builtins |

An opcode is not an intrinsic; it **calls into** one. `dim.get` is an opcode,
`php.ReadDim` is the intrinsic beneath it, and `strlen` is library.

**The membership test is mechanical.** An intrinsic touches values and a `Ctx`
and *nothing that knows what a class is*. Anything that needs a class table --
method dispatch, visibility, magic accessors, enum cases -- stays with the
consumer and reaches `runtime/php` through a hook, the way `ObjectProperties`
and `ResolveTraversable` already do.

Applied to the emitted prologue, that test partitioned it cleanly: 9 functions
were intrinsics written in the wrong place, and the other ~30 are legitimately
consumer-specific. The ratio matters -- the answer was not "move everything".

### Why the layer earns its keep

Moving those 9 required reading them, and `ReadDim` alone was wrong three ways:
it treated a **string as a non-container**, so `"abc"[1]` was null; it returned
null **silently** where PHP warns `Trying to access array offset on null`; and
it had no notion of a string offset's own rules, where a float offset *warns*
and a non-numeric string *throws*. A tenth row appeared while testing: in the
silent mode `??` uses, an out-of-range string offset is **not set** rather than
`""` -- the two modes ask different questions and so give different values, not
the same value with the diagnostic suppressed.

None of that was reachable from the interpreter, and none of it was tested,
because the code was duplicated into every generated program and owned by
nobody. That is the argument for the layer: **a shared contract gets read,
tested and fixed once; a private copy gets none of those.**

## The emitter translates; the catalogue decides

The pipeline is: the IR names an operation, the **catalogue** says which
type-variant of the intrinsic applies and what it owes, and `gogen` translates
that into a call to the Go function implementing it.

	add $a, 1                        an operation, in the compiler's vocabulary
	  |  lower.Specialize
	  v
	call.intrinsic "add", $a, 1      a CALL to a function in runtime/php
	  |  gogen
	  v
	php.Add(a, b)                    the Go function implementing it

An intrinsic is a **function**, so the IR contains a **call** to it. An
annotation naming the intrinsic on an arithmetic opcode is not the same thing:
it leaves the opcode primary and forces the emitter to keep a case per operator
to read the annotation from. With a real call the emitter has one case for the
family and re-derives nothing.

Everything about the operation belongs to the intrinsic, including the operator's
PHP spelling for `Unsupported operand types: float % string` -- the call carries
only a name and arguments.

**Forming the call is part of lowering. Choosing the variant is not.**

	lower.Lower  ->  IntrinsicCalls   an operation becomes a call    mandatory
	                 Specialize       the call's variant narrows     optional

`lower.Lower` runs `IntrinsicCalls` before it returns, so **there is no such
thing as a lowered function containing an `add` opcode** -- an emitter that
meets one is looking at IR built some other way, and it says so rather than
falling back. Every `.ir` golden is therefore in call form, because that is what
the IR is.

`Specialize` is optional in the honest sense: skipping it leaves correct code
that calls the general variant. It *is* a choice about the consumer -- an
interpreter over this IR would choose differently, or not at all -- which is
exactly why it must not be the step that decides whether the IR contains a call.

These were one pass at first, and conflating them made the call form itself look
optional: every golden showed bare operators, so the IR the design described and
the IR the goldens recorded were different languages.

`gogen` holds exactly one fact about these operations: `intrinsicFor`, the map
from opcode to intrinsic name. It lives on the compiler side because IR opcodes
are the compiler's vocabulary. Everything else -- the Go function, the effects,
the guard, the failure class -- is read from `php.Catalogue()`.

The chosen variant is recorded as the call's **callee**, which is why variants
are *named*: a choice that can only be described cannot be recorded, and a fact
that is not recorded is one the emitter has to re-derive.

**A compound assignment is a read, a call and a write.** `$x .= $y` arrives from
the harvester as one instruction with the operator as an attribute, because the
builder-promotion pass reads that shape; `IntrinsicCalls` expands it afterwards.
Keeping it whole through to the emitter meant a *second* operator table living
there, which drifted from the catalogue and left seven compound operators
uncompilable.

**Variants are selected by subset**, and the general variant has masks of
`MaskAny`, so it always applies. This is the property that makes inference
optional rather than required: a consumer that can prove nothing emits correct
wide code, and one that can prove `int` gets the pure variant without the
emitter changing. An operation is specialized by *narrowing what is known*, never
by the emitter learning a new rule.

Measured 2026-09-03: **4 of 2,277 intrinsic calls specialize (0.2%)**. The
mechanism is inert because inference tracks values and *a value that crosses a
statement is a place*, which this IR guarantees and the pass does not follow.
Extending inference to places is the next step and the one that would move it.

The corollary is a rule for adding an operation: **do not write it in the
emitter.** Add the intrinsic to `runtime/php`, catalogue its variants, and add
one line to `intrinsicFor`. An operation whose semantics live in `gogen` is
invisible to the other consumer and cannot be specialized.
