# Intrinsics and the IR's opcode set

**See the correction below**: this file was written calling the IR's opcodes
"intrinsics", and they are different layers. What follows is the opcode
catalogue; the intrinsic surface it calls into lives in `runtime/php`.

The alphabet of the compiler's lower-level IR. Started 2026-08-28 as Phase C0 of
the compiler track; see the Phase A results in `.agents-workspace/tmp/` for why
this is the first thing built rather than the last.

**Status: all eleven groups pinned, none verified by a differential case yet.**

*Pinned* means the signature was read out of the implementation and every
behavioural claim below was probed against the engine with `php2eval` — the
probes are quoted inline, so a wrong one is visible rather than implied. It does
**not** mean the contracts are covered by tests. Step 3 of "How an entry gets
pinned" is outstanding for every entry, and no specialization may key off any of
this until it is done.

Three claims in the first draft of this file turned out to be wrong when probed,
which is the argument for probing rather than transcribing: `instanceof` does
not autoload, `isset` is not effect-free, and `true`/`false` behave differently
under array autovivification.

## Correction, 2026-09-02: this file had the layers wrong

The first draft of this section said *"an intrinsic is a primitive of the IR"*.
**That is wrong**, and it conflated two layers that have to stay apart:

| layer | what it is | who has it |
|---|---|---|
| **IR opcodes** | the compiler's instruction set — `recv`, `slot.set`, `frame.set_state`, the call family | `internal/lower` only. A different back end would not want them. |
| **Intrinsics** | the essential builtin functions that **drive the runtime** — `Add`, `ArrayKey`, `Concat`, `Increment`, `CoerceArgument` | `runtime/php`. **Both consumers**, interpreter and compiled code. |
| **Library surface** | ordinary PHP functions — `strlen`, `array_map` | inherited, closed, typed from harvested signatures. |

An IR opcode is not an intrinsic; it **calls into** one, and many opcodes map
onto an intrinsic almost one-to-one, which is what made the two easy to blur.
`dim.get` is an opcode; the array read with PHP's missing-key rule is the
intrinsic underneath it.

The distinction is load-bearing rather than terminological, and it predicts
where code belongs: **an intrinsic is a shared contract, so it lives in
`runtime/php`; an opcode is compiler-only, so it lives in the emitter.** Every
time this project has moved a function into `runtime/php` — `Increment`,
`ArrayKey`, the diagnostics table, `Value.Copy` — it was recognising an
intrinsic that had been written in the wrong layer. `ArrayKey` is the clearest
case: it was duplicated in the generated program, and moving it was correct
precisely because it is an intrinsic and not an opcode.

The catalogue below is still a catalogue of **opcodes and their behavioural
contracts**, and those contracts are what the engine was probed for, so they
stand. Read the entries as "what the opcode must do", and expect the *doing* to
belong in an intrinsic wherever the operation is value-level.

## What the catalogue covers

The entries below are IR opcodes. An opcode is **not** a PHP function, and it is
**not** one of the 305 global functions `runtime/interp` implements — those are
ordinary library code the compiler links against, and their types come from the
harvested signatures. Keeping those apart matters too: the library surface is
inherited and closed, the opcode set is ours and small.

Every intrinsic carries a two-part signature.

**Representation signature.** What its operands and result *are* in emitted Go:

```
i64   f64   bool                       -- unboxed scalars
*php.String  *php.Array  *php.Object  *php.Resource  *php.Ref
zval                                   -- the boxed php.Value; the fallback
```

The IR performs arithmetic, comparison and control flow natively on
`i64`/`f64`/`bool` only. Strings, arrays, resources and objects are opaque
handles and every operation on one is an intrinsic call. No PHP type appears in
the IR's type system; which of `null|false|true|int|float|string|array|object|
resource` a value holds is an *analysis fact* attached to SSA values, and its
only job is to choose which specialization may fire.

**Effect contract.** What the intrinsic can do besides produce a value:

| effect | meaning |
|---|---|
| `print` | writes to output |
| `diagnose(...)` | emits named diagnostics, **in the order given** |
| `throw` | raises a PHP Throwable |
| `autoload` | may run an autoloader, which can print and throw |
| `reenter` | may call back into user code (`__toString`, a hook, a comparator) |
| `pure` | none of the above |

The contract is load-bearing, not documentation. **A specialization may fire
only where the contract says nothing is emitted at those operand types.** That
is the whole safety argument for the typed path, and it is forced by cases like
`"" % "2abc"` below.

---

## Arithmetic and bitwise — *pinned*

Source: `runtime/php/ops.go`, `runtime/interp/interp.go:8265-8300`
(`checkOperands`, `arith`, `operatorSymbol`).

| intrinsic | representation | effects |
|---|---|---|
| `add sub mul` | `(zval, zval) -> zval` | `diagnose`, `throw` |
| `div mod` | `(zval, zval) -> zval` | `diagnose`, `throw` (also division by zero) |
| `pow` | `(zval, zval) -> zval` | `diagnose`, `throw` |
| `bitand bitor bitxor` | `(zval, zval) -> zval` | `diagnose`, `throw` |
| `shl shr` | `(zval, zval) -> zval` | `diagnose`, `throw` (negative shift) |
| `bitnot` | `zval -> zval` | `throw` |
| `concat` | `(zval, zval) -> *String` | `diagnose(Array to string conversion)` |

### The evaluation order is the contract

`AcceptsArithmetic` (`ops.go:653`) walks `{a, b}` **strictly left to right** and
**stops at the first operand that cannot convert**. Both halves are observable:

| expression | diagnostics, in order |
|---|---|
| `2.5 % ""` | `Implicit conversion from float 2.5 to int loses precision`, then `Unsupported operand types` |
| `2.5 % "2abc"` | the same deprecation, then `A non-numeric value encountered` |
| `"" % "2abc"` | **only** `Unsupported operand types` — `""` fails first, so `"2abc"` never warns |

Converting both operands before deciding gets two things wrong at once: a later
operand warns when it should never have been reached, and an earlier operand's
diagnostic comes out after the one that follows it.

### Which operators diagnose a float

Only the integral ones — `mod`, `bitand`, `bitor`, `bitxor`, `shl`, `shr` —
call `warnPrecisionLoss`, because only they truncate (`ops.go:672-686`). `add`
keeps the float and says nothing. The deprecation is worded differently for a
float and for a string holding one, which is the only way a test can tell which
path it took (`ops.go:715-732`).

### Two shapes bypass numeric conversion entirely

- `add` on two arrays is a union, left operand winning on conflict
  (`unionArrays`, `ops.go:111`).
- `bitand`/`bitor`/`bitxor` on two strings combine bytewise.

Both are checked before the numeric walk (`ops.go:656-665`), so neither can
produce a numeric diagnostic.

### Specializations that are safe today

Each fires only when inference proves the operand types, and each is
diagnostic-free by the contract above:

| intrinsic | operand types | Go | result |
|---|---|---|---|
| `add` | `int, int` | `x + y` with the overflow check | `i64`, or `f64` on overflow |
| `sub` `mul` | `int, int` | likewise | `i64`/`f64` |
| `add sub mul` | `float, float` | native | `f64` |
| `concat` | `string, string` | `a + b` | `*String` |
| `div` | `int, int` | needs the zero *and* the exactness check | `i64`/`f64` |

**Integer overflow promotes to float rather than wrapping** (`ops.go:26`), so an
`i64` result type is only valid where the range is known not to overflow;
otherwise the specialization must keep the check and widen. `div` is not a
native division even at `int, int`: `4/2` is an int and `3/2` is a float
(`ops.go:71`), and `PHP_INT_MIN / -1` is excluded.

---

## Comparison — *pinned*

Source: `runtime/php/ops.go:128-490`.

| intrinsic | representation | effects |
|---|---|---|
| `loose_eq` | `(zval, zval) -> bool` | `reenter` (objects) |
| `strict_eq` | `(zval, zval) -> bool` | `pure` |
| `compare` (`<=>`) | `(zval, zval) -> i64` | `reenter` |
| `is_smaller` `is_smaller_or_equal` | `(zval, zval) -> bool` | `reenter` |
| `uncomparable` | `(zval, zval) -> bool` | `pure` |
| `bool_not` `bool_xor` | `-> bool` | `pure` |

PHP 8 corners, all three previously wrong here and settled against the engine
(`LTM/type-declarations.md:196-210`):

- A number compared with a **non-numeric** string compares **as strings**:
  `0 < "abc"` is true.
- An array is greater than any non-array: `0 < []` is true.
- `null` compares with a string as `""`, not through truthiness: `"0" == null`
  is **false**. And `null === false` is false — distinct types.

The trap is a shared predicate: `isBoolish` covers null, undef, true and false
and must not be used by `===`, which would inherit a looseness belonging only to
`==`. `isBool` and `isNullish` say which is meant.

Specializations safe today: `int, int` and `float, float` for all of
`loose_eq`, `strict_eq`, `compare`, `is_smaller`, `is_smaller_or_equal` → native
Go comparison. `string, string` for `strict_eq` → `==`. **Not** `string, string`
for the ordering operators, which go through PHP's numeric-string rules.

---

## Existence and type — *pinned*

Source: `runtime/interp/interp.go:1171-1310`, `runtime/php/ops.go:736-744`.
Every claim below was probed against the engine.

| intrinsic | representation | effects |
|---|---|---|
| `isset(place)` | `place -> bool` | `throw`, `reenter` (`__isset`, get hook) |
| `empty(place)` | `place -> bool` | `throw`, `reenter` (`__isset` **then** `__get`) |
| `is_type(zval, kind)` | `-> bool` | `pure` |
| `instanceof(zval, class)` | `-> bool` | `pure` |
| `type_name(zval)` | `-> *String` | `pure` |
| `get_type(zval)` | `-> *String` | `pure` |

`isset` and `empty` **share one opcode**; `extended_value & isEmptyCheck` picks
which. Neither is the negation of the other: `isset` is "exists and is not
null", `empty` is "missing or falsy".

### `isset` is not effect-free

The received wisdom that "isset never raises" is *almost* right and the
exception matters. On an array, an offset of a type that cannot be one throws:

```php
$a = [1]; isset($a[[]]);   // TypeError: Cannot access offset of type array in isset or empty
$a = [1]; empty($a[[]]);   // the same
```

Everything else about it is quiet, and each of these was probed:

| written | answer | why |
|---|---|---|
| `isset($str["z"])` | `false` | an unusable string offset is simply not set |
| `isset($c->i)` on an uninitialized typed property | `false` | the property exists but holds no value |

### The typed-property carve-out

A declared typed property that was never initialized is **not set, and the magic
accessors are not consulted** — it exists, it merely holds nothing. Only
`unset()`, which removes it outright, lets `__isset` have a say
(`interp.go:1238-1247`). A specialization that treats "no value" and "no
property" alike gets this wrong in both directions.

### Where the two diverge

`isset` is answered by `__isset` **alone**. `empty` also has to judge
truthiness, so on a `true` from `__isset` it goes on to call `__get`
(`interp.go:1262-1271`). A hooked property is different again: both run the
**get hook** and ask whether what it produced is null, so both can reenter and
throw (`interp.go:1214-1227`).

An inaccessible property reads as not-set rather than raising, and `__isset`
still gets its say (`interp.go:1229-1234`).

### `type_name` and `get_type` are different intrinsics

They disagree, so they cannot share an implementation:

| value | `type_name` (error messages) | `get_type` (`gettype()`) |
|---|---|---|
| `1.0` | `float` | `double` |
| `null` | `null` | `NULL` |
| an object | its class name | `object` |

`type_name` also renders `undef` as `"null"` (`ops.go:736-744`).

### `instanceof` does not autoload

Probed both ways — with an object operand and with a string operand, against a
registered `spl_autoload_register` that prints. **Neither triggers it**, and
both answer `false`. So `instanceof` is `pure`, unlike the resolution group it
superficially resembles. An earlier sketch of this file had it as `autoload`;
that was wrong.

---

## Places and access modes — *pinned*

A place is `Local | Dim | Prop | StaticProp | Append`. This is what the nineteen
indirection-producing opcodes become; holding write paths as places rather than
as temporaries carrying indirections is the specific thing Zend's own SSA cannot
express, and the reason `zend_ssa_var_info.indirect_reference` exists.

| intrinsic | representation | effects |
|---|---|---|
| `load(place, mode)` | `-> zval` | mode-dependent; `reenter` for `Prop` |
| `store(place, zval)` | `-> ()` | `diagnose`, `throw`, `reenter` |
| `bind_ref(place, *Ref)` | `-> ()` | — |
| `unset(place)` | `-> ()` | `reenter` (`__unset`) |
| `fetch_for_write(place) -> place` | | `diagnose`, `throw` — autovivifies |

**`mode` is part of the semantics, not a hint.** Probed:

| mode | on a missing `Dim` | on a missing `Local` | on a missing `Prop` |
|---|---|---|---|
| `R` | `Warning: Undefined array key "x"` | `Warning: Undefined variable $x` | `Warning: Undefined property: C::$p` |
| `IS` | silent | silent | silent |
| `W` | autovivifies — see below | — | — |
| `RW` | both | — | — |

`@` suppresses the `R` diagnostics without changing the value. `FUNC_ARG` is
undecided until the callee's signature is known: the interpreter resolves it
from the *preceding* `CHECK_FUNC_ARG` plus the pending-call stack
(`interp.go:4772`), so the lowering must resolve it statically or keep the
generic path.

### Write-mode autovivification has five outcomes, and `true`/`false` differ

`$a["x"] = 1`, by what `$a` currently holds — every row probed:

| `$a` holds | outcome |
|---|---|
| an array | silent |
| `null`, or undefined | silent, becomes an array |
| **`false`** | `Deprecated: Automatic conversion of false to array is deprecated`, then converts |
| **`true`**, int, float | `Error: Cannot use a scalar value as an array` |
| a string | `TypeError: Cannot access offset of type string on string` |

**`false` and `true` do not behave alike** — the first deprecates and proceeds,
the second is fatal. Any specialization of a `Dim` write over an operand merely
known to be `bool` is therefore unsafe; it needs the singleton type.

### String offsets are their own place

| written | result |
|---|---|
| `$s = "abc"; $s[1] = "Z"` | `"aZc"`, silent |
| `$s = "abc"; $s[5] = "Z"` | `"abc  Z"` — **padded with spaces**, silent |
| `$s = "abc"; echo $s[9]` | `Warning: Uninitialized string offset 9` |

A write past the end pads rather than failing, which means a `Dim` store on a
string is not a bounded operation and cannot be lowered to an index assignment.

### `store` and `bind_ref` are different operations

`$a = $b` writes *through* a reference the slot holds; `$a = &$b` **replaces**
the slot (`interp.go:8109-8133`). Backwards is silently wrong, and the 2026-08-28
aliasing fix (`JOURNAL.md`) is the precedent: *"the static spelling was correct
and only the dynamic one was not."*

### Edges carry provenance, not just shape

Two structurally identical `*php.Ref`s differ in whether reaching *through* one
earns `Attempting to set reference to non referenceable value` — the boxes the
engine made over a value nothing else names, such as a function's by-value
result, do (`borrowedRefs`, `interp.go:333-336`; the notice at
`interp.go:3047`). An IR node must therefore carry provenance, not only type.

---

## Coercions — *pinned*

Source: `runtime/interp/types.go:478-545` (`coerceScalar`, `doubleFitsLong`),
`runtime/interp/interp.go` (`arrayKey`), `runtime/interp/magic.go:147`
(`toString`). Probed against the engine.

These are intrinsics rather than casts because they diagnose, refuse, and can
re-enter user code.

| intrinsic | representation | effects |
|---|---|---|
| `to_bool` | `zval -> bool` | `pure` |
| `to_int` `to_float` | `zval -> i64`/`f64` | `diagnose` |
| `to_string` | `zval -> *String` | `diagnose`, `throw`, `reenter` (`__toString`) |
| `to_array` `to_object` | `zval -> *Array`/`*Object` | `diagnose` |
| `array_key(zval)` | `-> Key` | `diagnose` |
| `verify_arg(zval, type, n)` | `-> zval` | `diagnose`, `throw` |
| `coerce_return(zval)` | `-> zval` | `diagnose`, `throw` |

### `verify_arg` transforms; it does not assert

`int → float` satisfies the declaration in **both** modes *and* widens the
value. Treating it as a check leaves `float(1)` printing as `int(1)`
(`LTM/type-declarations.md:16`). The converted value must be written back.

Order, from `checkType`: an **exact match wins over any conversion**, which is
what keeps a string a string at `f(int|string $v)`. Only when nothing matches
does weak mode convert, preferring `int`, `float`, `string`, `bool` in that
order.

`strict_types` is decided by the **caller's** file, so it is a parameter of the
call rather than a property of the callee.

### Out-of-range is a refusal, not a truncation

`ZEND_DOUBLE_FITS_LONG` refuses outright — NaN, and anything outside
`[-2^63, 2^63)`. Only a float that *fits* but carries a fraction converts, with
a deprecation. Both probed:

```php
function f(int $x) {} f("21e50");  // TypeError — not PHP_INT_MAX
function g(int $x) {} g(1.5);      // Deprecated: Implicit conversion ... → int(1)
```

The wording differs between a float and a string holding one, which is the only
way a test can tell which path it took.

### What never converts

`null`/`undef` (a nullable declaration has already matched by then), any array,
and an object — the last is left unconverted rather than refused, because
`__toString` may yet make it a string and that decision belongs where the object
is (`types.go:483-489`).

### `array_key` is a diagnosing intrinsic

Every key coercion, whether the offset is being read, written, `isset` or
`unset`:

| key | result | diagnostic |
|---|---|---|
| `1.5` | `1` | `Implicit conversion from float 1.5 to int loses precision` |
| a resource | its id | `Resource ID#N used as offset, casting to integer (N)` — **every time** |
| `true`/`false` | `1`/`0` | none |
| `null`/`undef` | `""` | none |

The float comparison must go through the key's own conversion, not Go's cast: a
float too large for an `int64` wraps rather than truncating, and Go's conversion
is undefined out of range. And the number is spelled the way `var_dump` spells
one, at full precision — `9.223372036854776E+18`, not echo's
`9.2233720368548E+18`.

### `to_string` has three outcomes, not two

- A non-object converts. An **array warns** `Array to string conversion` and
  yields `"Array"` (probed).
- An object with `__toString` **re-enters user code**, so it can print, throw
  and recurse.
- An object without one is `Error: Object of class C could not be converted to
  string`. Every `Throwable` has one the engine supplies in C, so nothing is
  harvested for it and the class table cannot answer — that case is special-cased
  (`magic.go:160-166`).

**No specialization of `to_string` on an unknown object is safe.** Only a proven
non-object, non-array operand is diagnostic-free.

---

## Control flow and frame — *pinned*

Source: `runtime/interp/throw.go`, `generator.go`, `interp.go:2927-2975`
(`FAST_CALL`/`FAST_RET`), `runtime/php/ctx.go:366-385` (silence).

| intrinsic | representation | effects |
|---|---|---|
| `throw(zval)` | `-> !` | `throw` |
| `try_enter` / `try_leave` | region markers | — |
| `finally_call` / `finally_ret` | subroutine call | — |
| `yield(k, v)` / `yield_from(iter)` | suspends the frame | `reenter` |
| `iter_reset valid current key next free` | iterator protocol | `throw`, `reenter` |
| `bind_global(name)` | `-> place` | — |
| `bind_static(key)` | `-> place` | — |
| `bind_lexical(name, byRef)` | `-> place` | — |
| `silence_begin` / `silence_end` | `-> i64` mask | — |

### `finally` is a subroutine, and it can win

Every row probed:

| written | result |
|---|---|
| `try { return 1; } finally { echo "F"; }` | prints `F`, **returns 1** |
| `try { return 1; } finally { return 2; }` | **returns 2** — finally's return wins |
| `try { throw ...; } finally { return 2; }` | **returns 2** — and the exception is **swallowed** |

So `finally` is not a `defer`: it can replace the return value and discard an
in-flight exception. Zend models it as a subroutine call — `FAST_CALL` pushes a
**return address** into a temporary and `FAST_RET` jumps back — and
`frame.unwinding` is a *stack*, because an exception can be raised while another
is already unwinding. A Go `defer` covers the common case and none of these
three.

### `yield from` does not renumber keys

```php
function g() { yield from [1,2]; yield 3; }
foreach (g() as $k => $v) echo "$k=$v ";   // 0=1 1=2 0=3
```

The delegated keys are the **source's own** and the outer generator's counter is
unaffected, so `0` appears twice. `yield from` **proxies, it does not copy**:
storing what was pulled in the outer frame makes `current()` a snapshot, and two
delegates over one source then repeat each other's values.

`send()` is delivered by re-executing the store of the previous opline's result
(`generator.go:171-179`): `$x = yield 1` followed by `send("S")` puts `"S"` in
`$x`.

**A generator does not survive a straightforward lowering.** It is a saved frame
plus a pc, resumed by re-entering the executor. The two workable shapes are a
goroutine with channel handoff — which `fiber.go:132-135` already proves — or a
CPS/state-machine transform, which is where our own IR pays off. Both interact
badly with `finally` across a yield, since an abandoned generator must still run
its finallys.

### `@` changes the reporting, not the value

`@$u; echo error_reporting();` yields `30719` — the mask is restored on leaving
the suppressed expression, and a later `$undef2` still warns. The subtlety to
preserve is the other direction: an `error_reporting()` call made *inside* a
suppressed expression un-suppresses the rest of it.

### `global` and `static` are places, not copies

`global $x` binds the slot to a shared box from `ctx.Globals`
(`interp.go:791-797`) — one line to emit. `static $x` needs a per-`Ctx` home
keyed by `(op_array, name, scope)`; a Go package-level variable would break the
shared-nothing rule `runtime/php` is built on, and `Ctx` has nowhere to put one
today. That is a Phase B API addition.

---

## References — *pinned*

Source: `runtime/php/ref.go`, `runtime/php/array.go:361-421` (`Clone`).

| intrinsic | representation | effects |
|---|---|---|
| `make_ref(place)` | `-> *Ref` | `diagnose` (see provenance, above) |
| `deref(zval)` | `-> zval` | `pure` |
| `ref_share(*Ref)` | `-> ()` | `pure` |
| `ref_unshare(*Ref)` | `-> ()` | `pure` |

A `php.Ref` is a shared box with a **share counter**, not a pointer
(`ref.go:10-22`). `shared` counts the bindings an explicit `&` in the program
made, and is what distinguishes those from the boxes the runtime creates
internally to write through a path — the latter are invisible to any program.
Every emitted `$b = &$a` needs `ref_share`, and every frame exit needs the
`ref_unshare` sweep the interpreter does at `interp.go:479-484`.

`Ref.Set` stores `v.Deref()`, so chains cannot form.

### An array holding a reference is never uniquely owned

The load-bearing constraint for the ownership axis, probed:

```php
$a = [1,2];
$r = &$a[0];
$b = $a;        // a copy
$r = 9;
var_dump($b[0]);  // int(9)   -- the copy saw it
```

`Array.Clone` deliberately preserves a program-visible reference as **the same
box** (`array.go:392-406`), so copying an array does not sever it. Any
uniqueness claim over an array must therefore carry a "contains a reference"
bit; `Array.RefHeld` is the existing dynamic form of the same question.

## Construction — *pinned*

| intrinsic | representation | effects |
|---|---|---|
| `array_new()` | `-> *Array` | `pure` |
| `array_set(*Array, Key, zval)` | `-> ()` | `pure` (the key coercion is `array_key`) |
| `array_append(*Array, zval)` | `-> ()` | `throw` (integer key overflow) |
| `array_unpack(*Array, zval)` | `-> ()` | `diagnose`, `throw` |
| `new(class, args)` | `-> *Object` | `autoload`, `throw`, `reenter` |
| `clone(zval)` | `-> *Object` | `throw`, `reenter` (`__clone`) |

### `clone` re-enters and copies deeply

```php
class C { public $a = [1]; function __clone() { echo "CL"; } }
$x = new C; $y = clone $x; $y->a[] = 2;
count($x->a);   // 1 -- the array property was copied, not shared
```

`__clone` runs and can print, throw and mutate. `clone` on a non-object is
`Error: __clone method called on non-object` — note the wording names `__clone`
even when the class has none.

### Array unpack does not merge like `+`

Probed, and it is the opposite of `add` on two arrays:

| written | result |
|---|---|
| `[...["a"=>1], ...["a"=>2]]` | `["a" => 2]` — **later wins** |
| `[...[5=>"x"], ...[5=>"y"]]` | `[0=>"x", 1=>"y"]` — **integer keys renumber** |

`add` keeps the *left* operand's key on conflict and preserves integer keys;
unpack takes the *right* and renumbers. Two different operations that both look
like "combine two arrays", and sharing an implementation between them would be
wrong in both directions.

## Calls — *pinned*

Source: `runtime/interp/call.go`, `runtime/interp/interp.go:5969-6090`.

| intrinsic | representation | effects |
|---|---|---|
| `call_fn(name, args)` | `-> zval` | `throw`, `reenter` |
| `call_method(*Object, name, args)` | `-> zval` | `throw`, `reenter` (`__call`) |
| `call_static(class, name, args)` | `-> zval` | `autoload`, `throw`, `reenter` |
| `call_dynamic(callable, args)` | `-> zval` | `autoload`, `throw`, `reenter` |

A call is one IR node. In the op_array it is three phases — `INIT_*`, `SEND_*`
× N, `DO_*` — and **every** `INIT_*` pushes, `ZEND_NEW` included, since a
constructor is invoked through the same protocol. Phase A learned that the hard
way: pushing only for `ZEND_INIT_FCALL` handed one call's return type to
another's result.

### Send mode is part of the node, not of the argument

Each probed:

| mode | behaviour |
|---|---|
| by value | **copies** — `f(array $a){$a[]=9;}` leaves the caller's array at its old length |
| by reference | binds a place — `f(&$a){$a=9;}` writes through |
| by reference, given a literal | `Error: f(): Argument #1 ($a) could not be passed by reference` — fatal, not a warning |
| prefer-ref | accepts either; `extract()` declares its array this way |
| unpack | `f(...$arr)`; **string keys become named arguments** |
| named | resolved against the callee's signature, so not until `DO_*` |

`f(b:2, a:1)` and `f(...["b"=>2,"a"=>1])` both yield `"1-2"` — reordering is by
name, and the two spellings agree.

**`ZEND_SEND_PREFER_REF` is collapsed to a bool in the harvest today**
(`TODO.md`), so prefer-ref cannot be distinguished from by-ref. Recovering it is
a prerequisite for lowering calls faithfully.

### Binding is callee-driven

The callee's own `RECV`/`RECV_INIT`/`RECV_VARIADIC` opcodes bind parameters from
the argument list, which is how defaults work and where `verify_arg` runs.
Emitting ordinary Go functions gets this for free from Go's parameter binding —
but only after by-reference parameters, named arguments and variadics have been
resolved statically. Where they cannot be, the generic path has to stay.

Arity is checked at the callee: `f($a,$b); f(1)` is
`ArgumentCountError: Too few arguments to function f(), 1 passed ... and exactly
2 expected`.

### A float with no fraction converts silently

`f(int $x)` given `5.0` yields `int(5)` with **no** deprecation; given `1.5` it
deprecates. The test is `f == trunc(f)`, not "is it a float" — so a
specialization keyed on the declared type alone is wrong.

## Resolution — *pinned*

| intrinsic | representation | effects |
|---|---|---|
| `fetch_class(name)` | `-> *Class` | `autoload`, `throw` |
| `class_const(class, name)` | `-> zval` | `autoload`, `throw` |
| `fetch_static_prop(class, name, mode)` | `-> place` | `autoload`, `throw` |
| `const(name)` | `-> zval` | `throw` |

An autoloader **can print and can throw**, so every intrinsic marked `autoload`
is effectful no matter what its operands are. None of them can ever be
specialized into a diagnostic-free form on an unresolved class name.

Probed against a registered `spl_autoload_register` that prints:

| written | autoloads? |
|---|---|
| `new Missing` | **yes** |
| `Missing::C` | **yes** |
| `Missing::$s` | **yes** |
| `Missing::m()` | **yes** |
| `$x = "Missing"; new $x` | **yes** |
| `MISSING_CONST` | **no** — a global constant never autoloads |
| `f(Missing $m)` given a `stdClass` | **no** |
| `$obj instanceof Missing` | **no** |

The last two are the interesting ones, and they are why `instanceof` and
`verify_arg` sit outside this group: a name that has not been loaded cannot be
an ancestor of a class that has, so the engine answers `false` without asking.
A type check against an unknown class is therefore `pure` with respect to
autoloading, while *naming* that class in any other position is not.

## Output — *pinned*

| intrinsic | representation | effects |
|---|---|---|
| `echo(zval)` | `-> ()` | `print`, `diagnose`, `throw`, `reenter` |

**One intrinsic, not two.** There is no `print`. An earlier draft listed one
yielding `i64`, on the grounds that `print` is an expression and `echo` is not —
but the engine has already made that distinction disappear before the harvest
sees it. `$r = print "x";` compiles to:

```
0  ZEND_ECHO    CONST:0
1  ZEND_ASSIGN  CV:0, CONST:1
```

`ZEND_PRINT` does not exist in this build at all. So `print` is not merely
*representable* as echo-then-constant-1 — that is literally what arrives, and an
intrinsic for it would never be reachable. Its value is `int(1)` whatever the
operand (`print ""` and `print 0` both yield `1`), which is why the constant
folds.

Worth generalising: the intrinsic set should be derived from what the op_array
contains, not from what PHP's syntax distinguishes. Anything the compiler has
already lowered is not ours to model.

`echo` is `to_string` followed by a write, so it inherits that whole contract:

```php
echo [1];              // Warning: Array to string conversion, then "Array"
echo new stdClass;     // Error: Object of class stdClass could not be converted to string
echo new T;            // T::__toString() runs, and its own echo comes out first
```

That last case is the one to remember: a `__toString` that prints interleaves
its output *before* the value it returns. `echo` is therefore not a leaf, and
reordering two `echo`s is not safe unless both operands are proven non-objects.

---

## How an entry gets pinned

1. Read the implementation and state the signature here.
2. Settle anything ambiguous with `php2eval`, never by reading `zend_vm_def.h`.
   Note that `php2eval` cannot currently settle a question whose output names a
   file — see the entry in `TODO.md`.
3. Add a differential case for each effect the contract claims, built so that it
   would fail if the effect were absent (`QUALITY_GATE.md:32-34`).
4. Only then may a specialization key off it.

Phase A measured that **81% of the type information available in a corpus comes
from the opcode alone**, not from dataflow. That is why this file exists before
the IR does: for the typed path, this catalogue is not preparation for the work,
it is most of the work.
