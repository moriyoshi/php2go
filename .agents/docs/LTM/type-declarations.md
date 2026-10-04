# Type Declarations

## Summary

Parameter and return types are checked where the compiler put the checks:
`ZEND_RECV` / `ZEND_RECV_INIT` / `ZEND_RECV_VARIADIC` for a parameter,
`ZEND_VERIFY_RETURN_TYPE` for a return. The declared type arrives as the
engine's own rendering, so every TypeError's wording — including the order of a
union's alternatives — comes from the harvest rather than from anything
reconstructed in Go.

## Key facts

| Rule | Detail |
|---|---|
| int → float | Satisfies the declaration in **both** modes, but the value *widens*. Treating it as an exact match leaves `float(1)` printing as `int(1)` |
| `__toString` | Part of the string type, not a coercion: it applies in strict mode too |
| null to an internal function | **Deprecated**, not a TypeError. The same value to a user function throws |
| Boolean in a message | Reported by its singleton type: `true given`, never `bool given` |
| Implicit end of body | `VERIFY_RETURN_TYPE` with an unused op1. For anything but `void`/`never` that is the error: "none returned" |
| Argument message | `f(): Argument #1 ($x) must be of type int, string given, called in FILE on line N` — the call site, taken from the innermost stack frame |
| Return message | `f(): Return value must be of type int, string returned` — no call site; the return is the callee's own doing |
| Uncaught display | `Exception::__toString` appends " and defined" to a TypeError whose message contains ", called in " |
| strict_types | Decided by the **caller's** file, not the callee's |

## Details

### Coercion order

An exact match wins over any conversion, which is what makes
`f(int|string $v)` keep a string as a string. Only when nothing matches does
weak mode convert, preferring `int`, `float`, `string`, `bool` in that order.

Refusals: a non-numeric string to int, anything from null (a nullable
declaration has already matched by then), an array, and an object that cannot
render itself. A float with a fractional part converts *and* deprecates —
`Implicit conversion from float 1.5 to int loses precision`, with different
wording for a float and for a string holding one, which is the only way a test
can tell which path it took.

### Engine functions are checked from the harvested signature

`Manifest.builtins` carries every parameter's declared type, so the check is one
function applied at the single point in `Interp.call` where all three dispatch
paths meet. The converted value is written back into the argument slice, since
the conversion is what the function is meant to receive — without that,
`strlen(new T)` type-checks and then measures the wrong thing.

The handful the compiler specializes into their own opcode — `ZEND_STRLEN`,
`ZEND_COUNT`, `ZEND_GET_TYPE`, `ZEND_GET_CLASS` — never reach the call path and
need the check repeated. `get_class()` is a trap there: with no argument its op1
is UNUSED, and reading it as an argument produces a TypeError about a null that
was never passed. It names the class whose *body* is running, not the object's.

### What is not modelled

An intersection type (`X&Y`) is accepted unchecked. Refusing would be worse than
missing the check: it would invent an error valid code did not earn. Likewise
`self` / `static` / `parent` in a type position accept any object, since which
class they name depends on a frame the checker does not have.

## Files

- `runtime/interp/types.go` — the whole checker: `verifyArgument`,
  `verifyBuiltinArgs`, `checkType`, `matchesType`, `coerceScalar`,
  `givenTypeName`.
- `runtime/interp/interp.go` — the `RECV` family and `ZEND_VERIFY_RETURN_TYPE`.
- `internal/zend/zend.go` — `php2go_fn_strict_types` and the modifier shims.

## Test coverage

`runtime/interp/differential_test.go`, in the block starting "Parameter and
return types". Every message was settled by running the case against the engine.

## Null for an internal parameter is a scalar allowance

In weak mode an internal function takes `null` for a non-nullable parameter and
only deprecates it, where a user function throws. That allowance is not general:
it comes from the ZPP macros that coerce, so it stops at `bool|int|float|string`
(and unions of those). `object`, `array`, `iterable`, `callable` and class types
refuse null with an ordinary TypeError.

```
strlen(null)         Deprecated: strlen(): Passing null to parameter #1 ($string) of type string is deprecated
abs(null)            Deprecated: ... of type int|float is deprecated
count(null)          TypeError: count(): Argument #1 ($value) must be of type Countable|array, null given
get_class(null)      TypeError: get_class(): Argument #1 ($object) must be of type object, null given
```

Strict mode removes the difference: null is refused like any other wrong type.


## A __toString that throws: where the exception goes

`__toString` is a coercion and a coercion can throw. What comes out depends on
the site, and the return case is the one that looks wrong and is not:

| site | outcome |
|---|---|
| argument, user or internal | the `__toString` exception |
| instance or static property | the `__toString` exception |
| **return** | the ordinary `Return value must be of type ...` TypeError, with the exception as its `previous` |

The method runs in every case. php2go parks the throw on the context --
`Ctx.ParkError`, the same slot a throwing user error handler uses -- because
`checkType` returns `(value, bool)` with nowhere to put an error; each site that
was about to report a type error calls `coercionThrow(ctx)` first. Forgetting a
site is not silently wrong: the interpreter loop picks a parked error up at the
next opcode boundary, one opcode late.


## A numeric string keeps the type it parses to

Weak-mode coercion into a union tries the alternatives in a fixed order -- int,
float, string, bool -- and that is right for every source **except a string**. A
numeric string is converted to the type it *parses* to, when the union offers
it:

```
function f(int|float $v) {}
f("42.0");   // float(42)
f("42");     // int(42)
f("42.5");   // float(42.5), with no precision deprecation
f("1e3");    // float(1000)
```

Taking the fixed order for a string turns `"42.0"` into an int and reports a
precision deprecation for `"42.5"` -- both wrong, and both quiet.

## catch is an assignment

`catch (Exception $ref)` writes into `$ref`, so a variable that is a reference
to a typed property is held to that type there as anywhere else. The TypeError
*replaces* the exception being caught rather than joining it: the catch has
already consumed it.


## Pitfalls

- The check uses the *callee's* `strict_types` flag, which is identical within
  one file and wrong across files. Recorded in [TODO.md](../TODO.md).
- Passing an argument by reference must not convert it: the caller's slot is
  what the callee writes through, so a coerced copy breaks the binding.
- A default value is not checked. The compiler already accepted it, and
  re-checking it would reject `function f(int $x = null)`, whose harvested type
  is `?int` precisely because PHP widened it.

## Where a declared type is checked

| Site | Checked | Note |
|---|---|---|
| Parameter | `verifyArgument` at `RECV`/`RECV_INIT` | a by-reference parameter is bound, not converted |
| Return value | `VERIFY_RETURN_TYPE` | — |
| **Property assignment** | `verifyPropertyWrite` at `ASSIGN_OBJ` | added 2026-08-24; before that a typed property accepted anything |
| **Static property assignment** | `verifyStaticPropertyWrite` at `ASSIGN_STATIC_PROP` | declaration comes from the class, wording is the same |
| **Compound assignment** | the same, applied to the *result* | `$o->typedInt .= "x"` refuses |

All three share `checkType`, which coerces in weak mode and refuses in strict.
The property form words its failure differently: `Cannot assign string to
property T::$i of type int`, naming the *declaring* class.

The declared type reaches the property check through `php.Object.Declared`,
which `instantiate` fills for every typed property whether or not it has an
initializer — it is the same map the uninitialized diagnostic reads.

`declare(strict_types=1)` decides coercion for **arguments** from the file
containing the *call*, not the one containing the function; only the return type
uses the callee's own setting. `pendingFrame.strict` carries the call site's
value to the frame, since the callee's op_array cannot say it.

## A reference to a typed property is itself typed

`$x = &$o->typedInt` does not make `$x` an int; it makes the **shared box**
carry the property's type, and every name bound to that box faces it. The engine
keeps this as the reference's *type sources* (`zend_reference.sources`,
`ZEND_REF_HAS_TYPE_SOURCES`); php2go keeps `php.Ref.sources []RefSource`, added
in `elementRef` when the slot handed out belongs to a declared, non-dynamic,
typed property.

The diagnostics name the property, never the name being assigned:

| Situation | Message |
|---|---|
| Assignment the type refuses | `Cannot assign string to reference held by property C::$p of type int` |
| `++` overflowing an int | `Cannot increment a reference held by property C::$p of type int past its maximal value` |
| `--` underflowing an int | `Cannot decrement a reference held by ... past its minimal value` |
| The same on the property itself | `Cannot increment property C::$p of type int past its maximal value` |

Every write path that can reach a shared box has to check: the CV store
(`ZEND_ASSIGN`), compound assignment, `++`/`--`, and the object-property and
array-element forms of each. Missing one is not a missing error but a *wrong
value*, since the check is also what coerces (`$alias .= "1"` on an `int` source
stores `int(21)`, not `string("21")`).

`zend_incdec_typed_ref` decides the overflow case on the **shape of the step** —
an int that became a float, held by a source that does not accept floats — and
not on the resulting value. A range check gets `PHP_INT_MIN--` wrong, because
`(double)PHP_INT_MIN` is exactly representable and so passes one.

## Out-of-range floats do not coerce to int

`ZEND_DOUBLE_FITS_LONG` is a refusal, not a truncation. `zend_parse_arg_long_weak`
fails outright for NaN and for anything outside `[-2^63, 2^63)`; only a float
that fits but carries a fraction gets `Implicit conversion from float %s to int
loses precision`. The same applies to a numeric string that parses as a double,
which is why `$typedInt = "21e50"` is a TypeError rather than `PHP_INT_MAX`.

## Intersection and DNF types

A declared type is a **disjunction of conjunctions**, and PHP nests exactly one
level — so disjunctive normal form is the whole story:

```
int|string   -> [[int], [string]]
X&Y          -> [[X, Y]]
(X&Y)|(W&Z)  -> [[X, Y], [W, Z]]
?(X&Y)       -> [[X, Y], [null]]
```

`typeConjunctions` parses it; `checkType` accepts a value when it satisfies
**every** atom of **some** alternative. Coercion is reachable only through a
single-atom alternative — nothing converts to an intersection.

Variance uses the same shape: one alternative admits another when every type the
*wider* one names is satisfied by something the *narrower* one names. So `X&Y`
narrows `(X&Y)|W`, and `X` does not narrow `X&Y`.

Until 2026-08-24 both paths bailed out on `&`, the check accepting unchecked and
the variance reporting undecidable. The comment defending it said refusing would
invent an error the program did not earn — but accepting invents one too, and
the class table can answer "instance of both" perfectly well. When a check is
skipped, write down what it would take to do it, not only why it is skipped.

## Typed class constants

PHP 8.3's `public const string S = ...`. The type lives in
`zend_class_constant.type`, harvested as `ClassConstant.type` since 2026-08-24.

- The check happens **where the constant is read**, not where it is declared:
  the initializer may name something that only exists by then, as in
  `const string BAD = LATE_C;` with `define('LATE_C', ...)` after the class. The
  refusal is `Cannot assign stdClass to class constant A::BAD of type string`,
  raised on every read rather than memoised.
- The type is part of trait-composition compatibility. Two contributions of the
  same constant name conflict unless visibility, finality, **type** and value
  all match — `?array` beside untyped is a conflict.

## Interfaces are checked like classes

`interface B extends A` is subject to the same variance rules as a class
extending a class. `verifySignatures` used to return early for interfaces, with
no comment saying why; six of the `invalid_covariance_intersection_to_union`
tests declare the child as an interface and were unreachable because of it.

A guard with no stated reason is worth re-deriving rather than trusting.

## Comparison corners

Found by the matrix probe, all three previously wrong:

- **PHP 8 compares a number with a *non-numeric* string as a string.** `0 < "abc"`
  is true (`"0"` sorts before `"abc"`); PHP 7 cast `"abc"` to 0 and said false.
  php2go had applied this to `==` and not to the ordering operators — a
  half-applied language change is harder to spot than an unapplied one.
- **An array is greater than any non-array.** `0 < []` is true; there is no
  ordering between the kinds, so PHP puts arrays last.
- **null compares with a string as `""`, not through truthiness.** `"0" == null`
  is **false**. And `null === false` is false: distinct types.

The trap in the last one is a shared predicate. `isBoolish` covers null, undef,
true and false, and was used by both `==` and `===` — so `===` inherited a
looseness that belongs only to `==`. `isBool` and `isNullish` now say which is
meant.

## Arithmetic operands convert left to right

And the walk **stops** at the first operand that cannot convert:

| Expression | Diagnostics |
|---|---|
| `2.5 % ""` | precision-loss deprecation for `2.5`, then `Unsupported operand types` |
| `2.5 % "2abc"` | deprecation for the left, then `A non-numeric value encountered` for the right |
| `"" % "2abc"` | **only** the TypeError — `""` fails first, so `"2abc"` never warns |

Converting both operands before deciding gets two things wrong at once: a later
operand warns when it should never have been reached, and an earlier operand's
diagnostic is emitted after the one that follows it. `AcceptsArithmetic` walks
`{a, b}` in order and returns at the first failure, with the int-conversion
deprecation emitted inside the loop rather than after it.

## `self`, `static` and `parent` are class names, resolved where they are checked

They cannot be resolved when a type is harvested -- which class they name
depends on the frame -- so every check site resolves them itself
(`frameType` in `runtime/interp/types.go`):

| keyword | resolves to |
|---|---|
| `self` | the class whose body is running; in a *property* type, the class that declared the property |
| `static` | the class the call was made on (late static binding) |
| `parent` | the running class's parent |

Accepting any object instead -- which php2go did until 2026-08-28 -- means a
`: static` return type never refuses anything, and neither does a `self`
parameter.

Whether the diagnostic shows the resolved name is **not** uniform, and was
probed:

- return type and parameter: resolved. `SC::u(): Return value must be of type
  SD, SC returned`.
- property: left as written. `Cannot assign stdClass to property PC2::$p of type
  PA2|self`.

`static` is a return type and a property type only: `function f(static $x)` is
`Fatal error: Cannot use the static modifier on a parameter`, because there the
word is read as the modifier.

## `__toString` is a coercion, not part of the type

An object with `__toString` satisfies a `string` declaration in **weak mode
only**. `declare(strict_types=1)` refuses it for an argument, a return and a
property alike, and for an internal function's argument too -- `strlen(new S)`
is a TypeError there.

This is easy to get backwards: the conversion feels like part of what `string`
means, and php2go carried a comment saying exactly that for a long time.

Related: once the check sites resolve `self`/`static`/`parent` themselves, a
keyword that still reaches the matcher is one no class scope could name -- an
unbound closure declared `: static`. Nothing satisfies it, and the message keeps
the keyword rather than a class name.

## A property default is verified strictly, and on every instantiation

`zend_verify_property_type` runs over the defaults each time an object is made,
not once when the class is resolved -- two `new B` of a class with a bad default
report twice.

The check does not use the file's `strict_types`: an int default widens to a
`float` property (which strict allows) but a numeric-string default is refused
for an `int` property (which only strict does). The widened value is what gets
stored, so `public float $f = FIVE;` holds `float(5)`.

Only a **deferred** default can fail this way. One the compiler could fold is
refused when the file compiles, so by run time it is already known good.

## unset() on a declared property removes the slot for *both* directions

A declared property that was `unset()` keeps its declaration -- `var_dump` still
prints `uninitialized(?int)` -- but is *absent* as far as access goes:

| access | with no magic accessor | with one |
|---|---|---|
| read | `Typed property C::$p must not be accessed before initialization` | `__get` |
| write | writes the slot back | `__set` |

The state to keep separate is **cleared** versus **never initialized**. A
declared property that was simply never written still has its slot: reading it
is the same uninitialized error, but writing it goes straight to the slot and
`__set` is not consulted.

## Explicit casts are not parameter coercion

`(int)`, `(float)`, `(array)`, `(object)` and `(unset)` have their own value
operations. They can reuse primitive conversions only where the engine gives
the same result. In particular, an out-of-range float value and a string
containing that float do not necessarily cast to the same integer, and an
object-to-int conversion warns and yields `1` rather than throwing the string-
conversion error.

Casting an object to an array is the point where property visibility mangling
becomes program-visible: public keys stay plain, protected keys become
`"\0*\0name"`, and private keys use the declaring class. Internal object
storage generally keeps source names and applies mangling during this cast.

`(object)` needs the program-owning layer to allocate and number the handle, so
the shared intrinsic can build the property shape while the generated unit owns
object identity. This is the same boundary used by clone-like operations.
