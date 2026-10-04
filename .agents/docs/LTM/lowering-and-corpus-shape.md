# Lowering and Corpus Shape

## Summary

What an op_array corpus actually looks like when you try to lower it, and the
traps that cost the most to rediscover. Measured over
`third_party/php-src/Zend/tests` (4020 harvested tests, 10,738 op_arrays,
130,187 oplines, php-8.4.24) by a prototype lowering that reached 95.9% of
bodies.

Two facts do most of the work. **Coverage must be counted in bodies, not
oplines** — one unhandled opline bails a whole function, and the two numbers
differ by nearly thirty points. And **an op_array is a flattened AST**, so a
lowering's job is un-flattening, not translation.

## Key facts

| Fact | Number |
|------|--------|
| Temporaries read exactly once | **90.6%** |
| op_arrays that are a single basic block | **66.1%** (92.5% are ≤4) |
| Irreducible control flow | **0 of 10,738** |
| The call protocol's share of all oplines | **43.9%** |
| op_arrays free of `$$x`/`extract`/`eval`/by-ref locals | **93.8%** |
| Distinct opcodes occurring | 194 of 208 |

## Coverage in oplines overstates progress, badly

| top N opcodes handled | of oplines | **of bodies** |
|---|---|---|
| 10 | 60.0% | **14.8%** |
| 40 | 87.7% | **59.3%** |
| 100 | 97.7% | **88.5%** |

Implementing "the head of the distribution" sounds like a 40-opcode job at 88%
done; it is a 40-opcode job at **59%** done. Reaching 88% of bodies takes 100
opcodes and full coverage takes essentially all 194. The tail is cheap per item
— 39 opcodes occur fewer than 20 times each — but you need all of it.

**Report coverage as a body count with a histogram of the opcode that stopped
each one.** That is the compiler's equivalent of the `unsupported` bucket, and
it makes the next thing to implement always visible.

## The multi-opline node groups

Each is one AST node the compiler flattened. The interpreter already
reconstructs every one of them at run time, in a side structure keyed by temp
slot — `frame.writeTargets`, `iterators`, `calls`, `literals`, `borrowedRefs`,
`silences`, `funcArg`. **Making those side structures into a static pass is the
lowering.**

| AST node | Flattened as |
|---|---|
| `f($x, $y)` | `INIT_*` + `SEND_*` × N + `DO_*` |
| operand 3+ of any node | a trailing `ZEND_OP_DATA` pseudo-opline |
| `"$a$b$c"` | `ROPE_INIT` / `ROPE_ADD` / `ROPE_END` over consecutive temps |
| `[$a, $b]` | `INIT_ARRAY` + `ADD_ARRAY_ELEMENT` × N, **the result slot also an input** |
| `foreach` | `FE_RESET` / `FE_FETCH` / `FE_FREE`, state keyed by the reset's slot |
| `@expr` | `BEGIN_SILENCE` … `END_SILENCE`, the temp holding an error mask |
| `try/finally` | `FAST_CALL` / `FAST_RET`, the temp holding a **return address** |
| `$a < $b` in a condition | fused into the branch; see `Opline.smart_branch` |

Adjacency is not always enough to recover them: `returnsCallResult`
(`interp.go:6644`) scans *backwards* four oplines for a producer, `ZEND_JMP_NULL`
reads *forward* to the last opline it jumps over because "only the final one says
what the whole chain was for", and `FETCH_DIM_FUNC_ARG` needs the preceding
`CHECK_FUNC_ARG` plus the callee's signature to know whether it is a read or a
write.

## Result slots that are not where you expect

The most expensive class of bug found while lowering, and all of the same shape:
**a result slot ignored because the writeback was assumed to go elsewhere.** Cf.
[zend-operand-encoding](./zend-operand-encoding.md), which catalogues the operand
half of the same problem.

| Opcode | Trap |
|---|---|
| `ZEND_ASSIGN_OBJ_OP` and the compound-assign family | Carry a **result slot**: `$x = ($y .= "a")` yields the assigned value. Emitting them as statements drops it. |
| `ZEND_VERIFY_RETURN_TYPE` with a **CONST** operand | Cannot verify in place, so the coerced value goes to the **result** slot. The check *transforms* — `function f(): int { return 1.0; }` widens. |
| `ZEND_VERIFY_RETURN_TYPE` with an **unused** operand | Not a check at all: it is the implicit end of the body, and for any declared type but `void`/`never`, arriving there *is* the error. |
| `ZEND_CAST` | The target type is in `extended_value`; without it `(string)7` and `(array)7` are indistinguishable. |
| `DECLARE_*` `op2` | Typed `IS_UNUSED` but carries an index into `OpArray.closures` — which holds conditionally declared **named functions** too, despite the name. |
| `Literal.KIND_BOOL` | Carries its value in **`int_value`**, not a field of its own. |

## Control flow is simpler than feared

**Zero of 10,738 op_arrays are irreducible**, so structured Go is the normal
emission path and a `for { switch state }` relooper fallback is insurance. PHP's
`goto` cannot jump into a loop, which makes a multi-entry cycle hard to build —
though not impossible, and a detector reporting zero should be validated on a
constructed case before the zero is believed.

Two thirds of op_arrays are a **single basic block**, so the emitter should
special-case the straight-line function.

`finally` is the one exception to a static CFG: `FAST_RET` returns to the address
`FAST_CALL` pushed, so Zend's own `zend_cfg.c` leaves those edges unmodelled.
**Inlining the body at each exit removes the problem** — measured cost 907
oplines, 0.70% of the corpus, worst case 4 sites — and it also gives `finally`'s
hard semantics for free: its own `return` replacing the try's is a `ret` in that
copy, and swallowing an in-flight exception is the exceptional copy ending in
`ret` instead of rethrow. Go's `defer` can express neither.

## Values that outlive their block are places

A value read in a block that did not define it does **not** need block arguments
in the general case. The rule that covers it is the one that already keeps
compiled variables as places: **a value outliving its block is a place, and
mem2reg promotes it later.** A pre-pass marking slots read before their
definition, mirroring each definition into a named local, costs no liveness
analysis and no edge rewriting.

The `foreach` iterator forced the same treatment — defined by `FE_RESET` in one
block, read by `FE_FETCH` in another, ended by `FE_FREE` in a third — which is
why the interpreter keeps it in a side map rather than a slot.

Block arguments remain right where they are the natural shape: a `yield`
resumption value and a caught exception both arrive **on an edge**.

## The four short-circuit forms: one shape, two meanings

`&&`, `||`, `??` and `?:` compile to the same shape. Evaluate op1; on the
short-circuit path leave a value in the result slot and jump past a fallback
opline that writes **the same slot**:

```
i    JMPZ_EX  $a, ->i+2, TMP:7      # or JMPNZ_EX / COALESCE / JMP_SET
i+1  BOOL     $b        -> TMP:7    # or QM_ASSIGN, for ?? and ?:
i+2  ...reads TMP:7
```

So the slot is defined in two blocks and read in a third, which makes it a
**place** under the rule above -- no block arguments, no phi. The dead store the
fall-through path leaves behind is what the engine does too.

What lands in the slot differs, and the opcode names do not say. Settled against
the engine, not read off the VM:

| expression | result | so the opcode |
|---|---|---|
| `0 && $b` | `bool(false)` | `JMPZ_EX` **boolifies** |
| `1 \|\| $b` | `bool(true)` | `JMPNZ_EX` **boolifies** |
| `0 ?? $b` | `int(0)` | `COALESCE` **copies through** |
| `"a" ?: $b` | `string(1) "a"` | `JMP_SET` **copies through** |

The fallback opline is the tell -- `BOOL` for the first two, `QM_ASSIGN` for the
last two -- but only in hindsight. Getting it backwards turns every `?:` into a
boolean, which no coverage count can see.

`ASSERT_CHECK` shares the shape without producing a value, and is a **run-time**
check of the assertions setting rather than a compile-time one. Folding it during
lowering would bake the harvesting build's `zend.assertions` into the artifact.

## Knowing a fact in the transformation is not knowing it in the analysis

`ZEND_ADD_ARRAY_ELEMENT`'s result slot is also an input: the engine reuses one
slot across a whole array literal. That was known, documented and handled in the
lowering case for it.

It was **not** known to `findCrossBlock`, the analysis deciding which slots
become places, which counted op1/op2 as reads and the result purely as a
definition. So any array literal spanning a block boundary -- which is any
literal containing a short-circuit, `[$a ?? $b, $c]` -- had an accumulator the
analysis believed nothing ever read, and the body bailed.

Worth 0.9% of CakePHP on its own, and the general form is the point: a fact
recorded in the transformation does not propagate to the analysis that feeds it.
`ZEND_ADD_ARRAY_UNPACK` is the other opcode in this class.

## What is worth specializing is not arithmetic

**PHP programs are not arithmetic-heavy, so measuring the typed path by
arithmetic measures the wrong language.** Counting only `add`/`sub`/`mul` and
the comparisons is a C compiler's instinct. Counting what PHP instructions
actually are gives a different and much larger answer:

| | sites | share of the 95,491-instruction IR | operand/container typed |
|---|---|---|---|
| arithmetic and comparison | 3,028 | 3.2% | 43.0% |
| **container and dispatch** | **11,240** | **11.8%** | **52.9%** |

So the PHP-shaped operations are **3.7× more common** and type *better*.
Specializations could fire on **6.2% of the IR** against 1.4% for arithmetic —
**4.6× the opportunity**, and on the corpus that was written off as
unrepresentative for having no arithmetic.

| site | count |
|---|---|
| `call.method` on a known object | 2,013 |
| `array.append` on a known array | 1,186 |
| `prop.ref` on a known object | 913 |
| `prop.get` on a known object | 632 |
| `dim.get` on a known array | 323 |

**These need only the handle kind — D2's information — not a scalar type.** A
packed array indexed directly instead of hashed, a property read as a slot
instead of a lookup, a method call bound instead of resolved.

The remaining step is class identity: "object" is not enough to devirtualize.
The IR already carries what decides it — `has_get`, `has_set`, `has_call`,
`allow_dynamic_properties` (`ir.proto:305-316`), flagged precisely because
*"such a class cannot be reduced to plain field access"* — and Zend's own
inference carries the same thing as `ce` plus an `is_instanceof` bit. Tracking
the class entry alongside the handle kind is the natural extension, and the 2,013
and 632 above are upper bounds until it exists.

## Arithmetic specialization, and why the corpus question was a red herring

Measured over the lowered IR: of 95,491 IR instructions, only **3,028 (3.2%) are
sites where a specialization could apply at all**, and 33.1% of those have both
operands typed. So the whole specialization table would fire on about **1% of the
IR**, 72% of that being `concat string,string`.

The reason is the corpus, not the language. **`ZEND_ADD`, `SUB`, `MUL`, `DIV`,
`MOD`, `POW` and `IS_SMALLER` do not appear in the top 45 opcodes** — `DIV`
occurs 14 times in 130,187 oplines, `POW` twice, `SPACESHIP` once. An engine test
suite tests semantics and error messages, not throughput; a loop adding integers
is exactly what it has no reason to contain.

This section is kept because the numbers are real, but note the framing above:
arithmetic is not where PHP spends its instructions, so a benchmark corpus
answers a question that does not decide much.

**Do not size the *arithmetic* path from `Zend/tests`** — measured against
`Zend/bench.php` and `Zend/micro_bench.php`, which are vendored in the submodule
and arithmetic-dense, specializable sites go from 3.2% of the IR to **12.5%**
and the firing rate from 1.05% to **3.7%**. Roughly 3.5×.

But the *hit rate* on those sites barely moves (33.1% → 29.8%), and chasing that
found a third limit that dominates both:

> **Zero of the 30 functions in `Zend/bench.php` declare a parameter type.**

Isolated on one function body, changing only the signature: `function work($n)`
gives 40% of its specializable sites typed; `function work(int $n): int` gives
**100%**.

**The typed path's payoff is a property of the PHP being compiled, not of the
compiler.** Ranked: source typing discipline (40% → 100%) beats corpus
arithmetic density (3.5×) beats inference completeness (29.8% → 36.6%).

Neither vendored corpus is the one that matters. `Zend/tests` has the types and
no arithmetic; `bench.php` has the arithmetic and no types. Arithmetic-heavy
code *with* declared types is unmeasured because none is vendored — and it is
the only configuration in which the typed path is clearly worth building.

What this does *not* touch: **representation narrowing applies to every value,
not only specializable sites.** 20.3% of definitions have a known handle kind —
`object` alone is 15.4% — and each is a 72-byte `zval` that could be an 8-byte
pointer. That needs no arithmetic and is measurable here. The design's split
between "which kind of thing" and "which exact scalar type" turns out to be the
split between *pays on any corpus* and *unmeasurable on this one*.

A caution on the arithmetic rule itself: `add int,int` types as `int`, but
**integer overflow promotes to float**, so a specialization needs the range or
must keep the check. Any count of specializable int arithmetic is optimistic at
exactly the sites it reports.

## The runtime boundary

> `runtime/php` is the **data structure** — `Array.Get`/`Set`/`Append`, `Value`,
> `Ctx`, the operators. `runtime/interp` is the **semantics** — what a dimension
> *access* means.

Code outside `runtime/interp` can build a PHP array and **cannot read one
faithfully**: `arrayKey`, `dimensionRead`, `quoteKey`, `stringOffset` and the
uncaught-fatal rendering are all unexported and welded to `*frame`, `*Unit` or
`*pb.Opline`.

Reproducing them elsewhere is not merely duplicative, it is **silently wrong**.
A copy of `arrayKey` that omitted the float case made `$a[1.5]` use the string
key `"1.5"` instead of the integer `1` — wrong diagnostic *and* wrong value —
after twenty-two other cases had passed. Copied semantics do not announce that
they are approximations.

## Pitfalls

- **`engine.Reset()` between tests is mandatory** when one engine serves a
  corpus (`cmd/php2phpt/main.go:143`). Without it the second test to declare a
  class name fails to compile, and a third of the corpus lands in a bucket that
  reads as "these tests are unusual".
- **Every `INIT_*` pushes a pending call, and so does `ZEND_NEW`** — a
  constructor is invoked through the same three-phase protocol. A misaligned
  stack hands one call's return type to another call's result.
- **`cmd/php2harvest` does not print `extended_value`**, so its listing is
  misleading rather than incomplete for every opcode whose meaning lives there.
- **A coverage-limited measurement understates every limitation it has not yet
  reached.** Cross-block values looked like 0.6% of bodies at 59% coverage and
  2.3% at 92%, because bodies that stopped early never got far enough to need
  one.
- **When two tools disagree about the same input, instrument the failing one**
  rather than trusting the other. Sixty-two bails were logged as unexplained
  because a second tool was dumping a differently extracted file.

## Files

Design: [../IR-DESIGN.md](../IR-DESIGN.md),
[../IR-ASSEMBLY.md](../IR-ASSEMBLY.md), [../INTRINSICS.md](../INTRINSICS.md).
Throwaway measurement tools and their raw output lived in
`.agents-workspace/tmp/` (`lower/`, `cfg/`, `opfreq/`, `goemit/`) and are not
kept; the numbers above are the part worth keeping.
