# Intrinsics and Code Generation

## Summary

The compiled path has three layers that must stay distinct: the lower IR says
what the compiler needs to do, `runtime/php` owns shared PHP value semantics,
and `internal/gogen` translates already-selected operations into Go. An
intrinsic is a runtime function, not an IR opcode and not an emitter hint.

This separation was reached by repeatedly finding correct behavior duplicated
inside generated programs. The stable rule is: mandatory lowering forms the
intrinsic call, optional specialization chooses a named variant, and emission
only renders that choice.

## The three layers

| Layer | Owns | Does not own |
|---|---|---|
| `internal/lower` | CFG, places, calls, frame operations, PHP-opcode translation | PHP value semantics |
| `runtime/php` | values, references, arrays, coercion, diagnostics, intrinsic contracts | program class/function tables |
| `internal/gogen` | deterministic translation of lowered IR to Go | semantic rediscovery or variant selection |

Ordinary library functions are a fourth surface. Their public signatures come
from the linked engine and their implementations may be native or harvested
PHP; they are not compiler intrinsics merely because Zend specializes some of
their calls into opcodes.

## The lowering pipeline

The order is load-bearing:

1. Build structural IR and its CFG.
2. Convert value-level operations to `call.intrinsic`. This is mandatory: it
   records what the operation is.
3. Make reads explicit. Reading can warn and dereference a box, so it cannot be
   hidden in operand rendering.
4. Promote eligible places with mem2reg. Merges are block parameters, and the
   file body is excluded because its compiled variables are the global symbol
   table.
5. Infer types to a fixed point, then optionally replace a general intrinsic
   with a named variant such as `add.int`.
6. Emit the selected IR without making new semantic decisions.

`IntrinsicCalls` and `Specialize` were once one pass. Splitting them matters:
forming a call is consumer-neutral lowering, while choosing a representation
variant depends on the consumer and the facts its analysis proved.

## Intrinsic contracts

Every catalogue entry needs both a representation signature and an effect
contract. Effects include output, diagnostics, throwing, autoloading and
re-entry into user code. A narrow variant is sound only when it preserves all
observable effects for the selected operand types.

The Go binding must be able to perform the effects the catalogue claims. A
`ReEnters` or `Throws` entry needs a context and an error-bearing return shape;
otherwise the declaration is fiction. Conversely, a pure variant must not
retain a context merely because the general form has one.

Named variants make the selection inspectable and serializable. Metadata on an
old opcode left the emitter in charge; a distinct intrinsic name makes the pass
output the whole decision.

## Places, explicit reads and representation types

A place is an address-like value, not the value stored there. The same place can
be consumed for a warning read, a silent read, a write-through, a binding, or a
box handoff. Any opcode whose fetch mode could not settle what the eventual
consumer needs must preserve the place until that consumer.

This is why `read_var` is explicit and why call arguments, return-by-reference,
property receivers, nested dimensions and intrinsic arguments all need
place-aware paths. Rendering a place as if it were a local variable creates
valid Go that reads or writes the wrong storage.

Representation-only types are expensive. The iterator cursor once cost an IR type,
a special frame slot and a raw argument path. Wrapping it in an internal PHP
object made it an ordinary `php.Value` and removed all three exceptions; use
that result as the pattern when another private representation leaks across layers.

## Type inference and specialization

- Unknown is not bottom. Treating it as an absorbing element prevents a
  loop-carried value from ever learning anything from its other incoming edge.
  The converse bites too: the fixed point reads "no fact yet" as bottom, which
  is sound only if every value that can never get a fact is seeded unknown.
  An untyped parameter, a read of an untracked place and an unregistered
  constant have rules that *leave* no fact, and were not seeded -- so a join
  of an untyped parameter with `1.5` was proved a float and `"abc" + 1`
  skipped its TypeError. `unanswerable` seeds them; a new rule that can
  decline to answer must be listed there.
- Container types carry key and element types. Their subsumption is covariant;
  a bare `array` means unknown contents, not an array of bottom.
- Engine constants are typed from the harvested value. Program constants need
  a join over every declaration of the name.
- Places require their own type flow. A promoted SSA value and an unpromoted
  file-body place are not interchangeable evidence.
- Measure only operations that have a narrow variant. Counting `concat` calls
  against an arithmetic-specialization rate made the corpus shape look like an
  analysis failure.

## Unboxed representations

`lower.IntReps` and `lower.FloatReps` give a value a Go `int64` or `float64`
instead of a `php.Value`. They run after every other pass (the choice is about
the final instructions), ints first, and skip resumable bodies, whose
suspension-crossing values already live in typed frame slots. Places stay
`php.Value`: a place is reached by name, by reference and by `compact`, so a
store boxes (`placeTypes` widens both representations to `zval`).

- **An int64 boxes exactly**, so an int value may be used anywhere, boxed per
  use. The hard part is that `++` on PHP_INT_MAX yields a float: a step is
  native only when its block is reached solely through the true edge of `x <
  y` (int `y`) on the **same SSA value** it steps. "Solely" means the target
  has one predecessor; a `goto` into the guarded block was the case that
  proved it.
- **A float64 may also carry an int**, when the int comes from a small known
  set of constants: `$z = 0` joined with a float is the loop shape every
  float benchmark has, and a float-only rule narrowed 2 of mandel2's 11 hot
  values. Int-with-int arithmetic on those constants is kept only if integer
  and double results agree bit for bit, which rejects overflow and signed zero
  (`0 * -3` is int 0, `0.0 * -3.0` is `-0.0`). Such a value may not be boxed,
  since `float(0)` is not `int(0)`; only a value that is never an int may.
- **Every native float result is wrapped in `float64(...)`**. The Go spec lets
  the compiler fuse `x*y + z` into one multiply-add, across statements, and on
  arm64 it does; the conversion is the one way to forbid it. mandel2 drew three
  wrong characters before this. Fusion is still wanted for speed where the
  last bit does not matter, so `gogen.Program.FusedMath` (`php2gotest
  -fused-math`, `hack/bench.sh -F`) emits the expressions verbatim; graded
  against the engine it is expected to fail on exactly those cases.
- A Go constant has no negative zero: `float64(-0.0)` is +0, so a `-0.0`
  literal is spelled `math.Copysign(0, -1)`.
- A lattice evaluated recursively through operands is exponential on a long
  chain. Every candidate's state belongs in the fixed point, iterated in
  program order.
- **Overflow facts come from both edges of a branch.** The true edge of `a <
  b` is `a < b`; the false edge is `b <= a`, which is not strict, so with a
  variable bound it proves nothing (both can be PHP_INT_MIN) and a literal
  bound leaves only the room between it and the edge. The edge must be the
  only way into its target.
- **An inline fast path at the site beats a faster function.** Most hot
  runtime entry points (`ReadDim`, `getModAt`, the Iterator* trio, `Add`)
  cannot fit Go's inline budget of 80, so the emitter tests a small exported
  predicate (`PackedAt`, `PackedSet`, `PackedAppend`, `SnapIter`,
  `propSite.hit`, the boxed int test) and falls through to the unchanged
  general call. Each such predicate must decline anything the general path
  treats specially; its fixture owes a mutation per condition.
- **Untyped parameters get versions, not types.** Call-site inference is
  unsound here (`call_user_func`, `array_map`, reflection and the bundled
  library call functions by computed names), so `lower.IntVersions` clones a
  plain function assuming chosen untyped parameters are ints, and the general
  body's entry calls the clone when they arrived as plain ints. A version is
  a body in its own right: it must be in `gogen.Bodies` so every per-body
  analysis runs on it -- leaving it out dropped the ownership facts and made
  `issetor` copy its array per iteration (0.14 s to 4.6 s). The entry that
  chooses is a **separate small function**: a test at the top of the general
  body still pays for that body's frame -- every declared value zeroed, a
  deferred release on the way out -- and in ackermann that was more than
  the version's own work. A version taking every parameter as an int has an
  **unboxed entry** (`php_f__i(ctx, a0, a1 int64)`), called directly from
  sites whose arguments pass an inline kind-and-range test, and its frame
  keeps the ints unboxed until a reader of the stack materialises `Args`.

Determinism is part of correctness. A pass that assigns value numbers or emits
items while ranging over a Go map must sort first; nondeterministic IR cannot be
diffed, cached or used as a golden.

## Emission rules

- A refusal is returned as an error. A comment followed by `php.Undef()` is
  valid Go and converts a known gap into plausible output.
- A body is finished once its terminator is emitted. Later lowering must not
  append instructions to it.
- Operations hidden in terminators (`iter.valid`, null branches and return
  coercion were examples) should be split into explicit intrinsic calls when
  they have ordinary value semantics.
- A result slot can have more than one control-flow producer. Match and
  frameless-call fast/fallback paths both rely on that fact.
- A generated table containing callbacks into the generated program is filled
  at startup, not as a package-scope literal, to avoid Go initialization cycles.

## Relevant files

- `internal/lower/lower.go`, `reads.go`, `mem2reg.go`, `specialize.go`,
  `intrep.go`, `floatrep.go`, `versioning.go`,
  `types.go` — the mandatory and optional passes.
- `runtime/php/catalogue.go`, `intrinsics.go`, `generic.go` — shared contracts,
  variants and generic container types.
- `internal/gogen/gogen.go`, `constexpr.go` — translation and deferred literal
  rendering.
- `.agents/docs/INTRINSICS.md`, `IR-DESIGN.md`, `IR-ASSEMBLY.md` — canonical
  current design; this document records the durable reasons behind it.

## Test coverage

`internal/lower` goldens pin IR shape and `cmd/php2gotest/testdata` executes the
same constructs against the engine. Catalogue tests check binding shapes and
variant selection. A golden is not behavioral evidence, and a differential
case that never uses the produced value is not evidence either.

## Pitfalls

- An inlinable fast path makes its callers bigger. Giving `Value.Bool` one
  pushed `ToBool` over Go's inline budget of 80, and `&&` in mandel's inner
  loop then spent 47% of its time in the call. After changing a hot helper,
  check its callers with `go build -gcflags=-m=2` as well as the helper.

- Do not infer an intrinsic list by grepping the emitter; enumerate the surface
  and classify each operation.
- Do not specialize merely because the Go operation looks equivalent. Preserve
  PHP overflow, evaluation order, warnings, throws and re-entry.
- Do not treat a census label as a diagnosis. It names the place that refused,
  which may be far downstream of the missing representation.
- Correct stale architecture prose in place. Appending a correction produces a
  second inventory that will drift again.

## Consumers match semantic roles, not spellings

When one operation gains another IR spelling, consumers that matched the old
opcode usually do not fail; they silently stop finding anything. Moving local
writes from `local.set` to `write_var` and `bind_var` made type inference,
mem2reg, loop-builder discovery, place typing, liveness and CPS parked-value
analysis inert until they all used the shared `lower.StoreOf` recognizer.
Catalogue bindings likewise derive a variable name from the place for
`read_var`; the IR must not carry a second copy of a fact it already encodes.

The store variant is selected from the place's joined representation type, not
from the current value's type, because one place must have one storage shape
across all writes. An address such as `&p_x` is not a read and read-discipline
checks must ignore it. Correctness tests may not expose an analysis that safely
finds nothing, so keep a rate or shape assertion for consumers whose failure
mode is silent loss of optimization.
