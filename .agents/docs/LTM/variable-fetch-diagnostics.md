# Variable Fetches and Their Diagnostics

## Summary

PHP splits variable access into fetch *types*, and the diagnostics follow the
type, not the syntax. A write fetch creates its target silently; a
read-modify-write fetch warns first. Conflating them was php2go's largest single
source of spurious diagnostics — roughly 100 bogus `Undefined variable` warnings
across `Zend/tests`.

## Key facts

- **`BP_VAR_W` (write) is silent and creates.** `$a[0] = 1`, `$this->p[] = x`,
  `f($arr[])`, `$u->p = 1` never warn about the undefined container. The only
  error a write can raise is about the *assignment* —
  `Attempt to assign property "p" on null` — never about the read.
- **`BP_VAR_RW` (read-modify-write) warns, then creates.** `$c[0] .= "x"` emits
  both `Undefined variable $c` and `Undefined array key 0` before doing the
  work. Compound assignment reads first, so it reports like a read.
- **Taking a reference defines the target as null, silently.** `$c = &$undef`,
  a by-ref argument, `use (&$j)`.
- **A by-value `use ($i)` warns once, at capture**, and captures the null the
  read produced.
- **`false` still grows into an array**, with
  `Deprecated: Automatic conversion of false to array is deprecated`.
- **Read diagnostics name the type they found**, not a category:
  `Attempt to read property "q" on null` / `on string` / `on int`. Never "on
  non-object". The same holds for indexing: `Trying to access array offset on
  <type>`, where a boolean names itself `true` or `false`.
- **Indexing has a diagnostic for every wrong shape.** An array or object *as*
  an offset is `TypeError: Cannot access offset of type <T> on array`; a string
  offset that is not already an integer is cast and the cast reported
  (`String offset cast occurred`), except a non-numeric string, which is
  refused; writing through a non-container is `Cannot use a scalar value as an
  array`, `Cannot use string offset as an array`, or `[] operator not supported
  for strings` for an append.
- **A fractional float used as a key is deprecated wherever it is used** — read,
  write, `isset`, `unset`, `array_key_exists` — so the report belongs in the key
  conversion itself rather than at any one call site.
- **The IS and UNSET fetches never complain.** `ZEND_FETCH_IS` is what `isset()`
  and `??` compile to and `ZEND_FETCH_UNSET` what `unset()` does; the question
  already admits the answer may be "not set".
- **The by-name opcodes carry the name, not the variable.** `ZEND_UNSET_VAR` and
  `ZEND_ISSET_ISEMPTY_VAR` take op1 as the *name holder*: `unset($$n)` unsets
  what `$n` spells, not `$n`. The CV forms (`ZEND_UNSET_CV`,
  `ZEND_ISSET_ISEMPTY_CV`) are the direct ones.
- **A user error handler sees everything.** `set_error_handler`'s callback is
  called even when `error_reporting` would hide the level and even under `@`:
  the mask gates the *default* handler, not the callback. Only an explicit
  `false` return asks for the default text as well, a diagnostic raised inside
  the handler is not fed back to it, and `restore_error_handler` pops a stack.

## Details

`frame.slotRef` implements reference vivification, and it must normalize an
*existing* box holding undef, not only make a new one. At global scope every CV
slot is already a reference into `Ctx.Globals`, so the make-a-new-box path is
never taken there — a fix that only handled the new-box case would look correct
and do nothing where it mattered.

Storing undef for a by-value `use ($i)` rather than the null the capture-time
read produced makes the body re-warn on every read. The warning belongs to the
capture, once.

PHP 8 operand rules are the neighbouring cluster: a wholly numeric string
converts silently, one that merely starts with a number warns
(`A non-numeric value encountered`), and anything else is
`TypeError: Unsupported operand types: …`. Integer operators additionally
deprecate a lossy float or float-string conversion. Two arrays under `+` union;
two strings under `& | ^` combine bytewise.

`error_reporting()` is a real mask on `Ctx` that filters by level, seeded from
the harvested default rather than a transcribed `E_ALL`.

### Routing a diagnostic through PHP

`Ctx.raise` is called from places that cannot call anything — deep inside the
value model, with no unit and no interpreter — so `set_error_handler` needs a
hook (`Ctx.SetErrorHandler`) rather than a direct call, and a parked exception
(`Ctx.TakePendingError`) that the main loop picks up at the next opcode
boundary. That is one opcode later than PHP, which throws from the diagnostic
itself; nothing in the corpus can tell, but it is a real difference.

The location has to be snapshotted before the handler runs. The handler's own
body moves `Ctx`'s idea of where execution is, so a diagnostic that falls
through to the default printer otherwise reports the line of the *closure*
instead of the site that raised it.

## Files

- `runtime/interp/interp.go` — fetch handling; `frame.slotRef` and the
  read/write distinction.
- `runtime/interp/include.go` — `slotRef` across an include boundary.
- `runtime/php/ctx.go` — diagnostics and the `error_reporting` mask.
- `runtime/php/value.go`, `runtime/php/ops.go` — operand rules and conversions.

## Test coverage

`runtime/interp/differential_test.go` for the fetch-type behaviour;
`runtime/php/value_test.go` and `runtime/php/engine_parity_test.go` for
conversions, operand rules, and silenced diagnostics.

### References in the value model

`Array.RefAt` boxes an element **whenever a write descends through it**, not
only when a name binds to it: `$b[0][1] = 2` leaves `$b[0]` holding a `Ref` with
exactly one holder. Two consequences:

- "Holds a reference" is not "is shared". That is why `var_dump`'s `&` marker
  cannot be approximated from the box alone — it was tried and measured as a net
  loss. See [TODO.md](../TODO.md).
- Releasing a temporary must **unbind** the slot, never write through it.
  `ZEND_FREE` used `store`, and freeing the alias `[&$p] = $a` creates destroyed
  `$a` itself.


## `$GLOBALS` is a fetch by name, not an array access

`$GLOBALS["x"]` compiles to `ZEND_FETCH_*` against the global symbol table with
`ZEND_FETCH_GLOBAL` set -- there is no array to index. The fetch *type* decides
what a missing name earns, the same way it does for an ordinary variable, but
the wording differs so a test can tell the two apart:

| opcode | written as | missing name |
|---|---|---|
| `ZEND_FETCH_R` | `$GLOBALS["x"]` | `Warning: Undefined global variable $x` |
| `ZEND_FETCH_IS` | `??`, `isset()` | silent -- the absence *is* the answer |
| `ZEND_FETCH_RW` | `$GLOBALS["x"] .= ...` | reported, in the global wording |
| `ZEND_FETCH_W` | `$GLOBALS["x"] = ...` | created in silence |
| `ZEND_FETCH_UNSET` | `unset($GLOBALS["x"]["k"])` | silent, **and not created** |

The UNSET form is not a by-reference fetch even though it hands a slot to
`UNSET_DIM`: taking a reference to a name the table does not hold would grow the
table, and the engine leaves it alone. Ask `php2harvest` which opcode a form
compiles to before assuming.

## `$this` is not a compiled variable

The frame holds the instance directly, so `$a = "this"; $$a` -- the only way to
reach `$this` as a variable -- has to be answered from there rather than from
the slot table. A by-name **write** is refused with
`Error: Cannot re-assign $this`, and the refusal does not depend on there being
a `$this`: the same code in a plain function earns it. Reading and `isset()` are
allowed; `unset($$a)` is allowed too and really does drop it.


## Pitfalls

- Every rule above was settled by asking the engine through `cmd/php2eval`, not
  by reading `zend_vm_def.h`. See
  [differential-oracle](./differential-oracle.md).
- `$this->p[] = x` and friends look like ordinary variable fetches and are not:
  `$this` is an unused operand. See
  [class-model-and-harvesting](./class-model-and-harvesting.md).
- A spurious warning and a missing one are the same class of bug and both change
  the `.phpt` verdict; a fix must be checked in both directions.

## Unset is not uninitialized

A declared *typed* property can hold nothing for two different reasons, and the
engine distinguishes them with `IS_PROP_UNINIT` on a slot that exists either
way:

| State | `var_dump` | magic accessors | Reading it |
|---|---|---|---|
| Never assigned | `uninitialized(T)` | not consulted | `Error: Typed property C::$p must not be accessed before initialization` |
| `unset($o->p)` | `uninitialized(T)` | **consulted** | whatever `__get` returns |

Removing the slot gets the accessors right and loses the dump; keeping it gets
the dump right and loses the accessors. `php.Object.Cleared` records the removal
while the slot survives holding undef, and `Unit.slotAbsent` is what the
read/isset/write paths ask.

An *untyped* declared property disappears entirely on `unset`, as does a dynamic
one.

**Compiled output keeps the slot for both**, holding undef, and so has to ask
the same question with its own spelling: an undef slot whose declaration is
untyped *was unset* (an untyped declaration is never uninitialized) and reads
as absent -- `__get`, else `Undefined property`; a write to an undef slot
that `WasCleared` goes to `__set`. Both were wrong until 2026-09-27, and the
typed-unset read with `__get` still is (TODO). The compiled property fast
path relies on this: it never takes an undef slot, so every absent-or-unset
case falls through to the full path.

Two consequences elsewhere:

- A write that visibility refuses is offered to `__set` only when the slot is
  absent in this sense. Forwarding whenever the write is refused breaks the
  merely-uninitialized case.
- That forward must consult the magic re-entry guard (`Unit.magicAvailable`),
  or `$this->p = $v` inside `__set` forwards to `__set` again and quietly
  creates a dynamic property instead of raising.

## readonly refuses for two different reasons

| state | write from outside the declaring scope | write from inside |
|---|---|---|
| uninitialized | `Cannot modify protected(set) readonly property A::$p from global scope` | allowed |
| initialized | `Cannot modify readonly property A::$p` | same |

Before it holds a value, writing is an *initialization*, governed by the
set-visibility a `readonly` declaration implies — `readonly` sets
`ZEND_ACC_PROTECTED_SET` whether or not the source wrote it, and `public
readonly` is indistinguishable from `public protected(set) readonly` in the
flags. Once it holds a value, readonly's own rule applies to every scope.

Both messages name the **declaring** class, not the object's: `B extends A`
reports `A::$p`.

Two further rules:

- A W/RW/UNSET fetch of a readonly (or set-restricted) property that currently
  holds an **object** is allowed and yields a copy — `$o->prop->field = x`
  cannot modify the property itself. Same exemption as asymmetric visibility.
- `__clone` may reinitialize the clone's readonly properties, **once each**, and
  only while it runs (PHP 8.3). `php.Object.Reinitable` models the permission;
  the first write spends it and `EndReinit` withdraws the rest.

## Everything that prints goes through Ctx.Write

`Echo` and the diagnostic writer used to write straight to the output stream.
That was invisible until `ob_start` arrived and buffered nothing. Both route
through `Write` now, which is also what makes

```php
ob_start(); $x = @something_that_warns(); $warning = ob_get_clean();
```

work: a diagnostic is output like any other, and an open buffer captures it.

Anything new that emits must use `Write` for the same reason.

## Everything that wants a string goes through Interp.toString

`php.Value.String()` returns `""` for an object — the php package cannot run a
method. So every path that needs a *PHP* string conversion must call
`Interp.toString`: `ZEND_ECHO`, the `(string)` cast, `ZEND_CONCAT` /
`ZEND_FAST_CONCAT`, the `ROPE_*` opcodes (interpolation), and the loose
comparison operators.

| Rule | Note |
|---|---|
| An object converts only through `__toString` | a class without one is `Object of class N could not be converted to string` |
| `__toString` has an implicit `string` return type | since PHP 8 — a body with no `return` raises `Return value must be of type string, none returned` |
| A **loose** comparison renders the object | `$s == "S!"` is true; `===` compares types and never does |
| Two objects compare as objects | rendering only applies when exactly one side is a string |

Until 2026-08-25 none of this happened: `stringify` was correct but had a single
caller (`checkType`, for parameter and return types), so `strlen($obj)` worked
and `echo $obj` printed nothing.

The shape that hid it: `stringify` returned `(value, ok)` and swallowed the
error from the method it invoked, so a caller could not tell "no `__toString`"
from "`__toString` threw". A helper whose only failure signal is a bare `false`
invites being used in one place and forgotten.

## Overloaded properties in write position

`$o->arr[] = 1` where `arr` exists only through `__get` has no slot to alias.
The engine runs the accessor and then:

- warns `Indirect modification of overloaded property M::$arr has no effect`,
  attributed to the **modification site** — the accessor is a call, and a call
  moves the reported location, so it has to be saved and restored around it;
- **unless what `__get` returned is an object**, in which case it is silent:
  modifying the object reaches the same instance, so nothing is lost. This is
  the same exemption hooked properties get in `zend_std_read_property` under
  `BP_VAR_W`, and `bug34893` exists to check it.

`$o->p ?? x` asks **`__isset` first** and only calls `__get` when it answers
true — which is why `isset($o->p)` can be false on a class whose `__get` would
happily produce a value.

## String offsets: read, isset and write disagree

The three operations give different answers for the same index, so a probe has
to exercise all three:

| Index | read | isset / empty | write |
|---|---|---|---|
| `2.7` | `Warning: String offset cast occurred` | `Deprecated: Implicit conversion from float 2.7 to int loses precision` | as read |
| `-6` on `"hello"` | `Warning: Uninitialized string offset -6`, yields `""` | `false` | `Warning: Illegal string offset -6`, string **unchanged** |
| `"1abc"` | `Warning: Illegal string offset "1abc"`, then offset 1 | `false` | as read |
| `"1.5"`, `"1.0E+121"` | `TypeError` | `false` | `TypeError` |

Two traps:

- The negative write warns with the offset **as written** (`-6`), not the
  adjusted one, and it is a *warning* — the string is left alone rather than the
  write being an error.
- `"1abc"` works and `"1.5"` does not. The test is whether `is_numeric_string`
  recognises the whole string: a wholly numeric non-integer is refused, and only
  a string that *stops* being a number partway through contributes its prefix.
  "starts with digits" is the wrong test and passes `"1.5"`.

## A reference in a result slot must be *bound*, not stored

`FETCH_LIST_W`, `FETCH_DIM_W` and every other opcode that produces a reference
have to use `bindResult` (which replaces the slot) rather than `storeResult`
(which writes *through* a reference the slot already holds).

The bug this prevents only appears the **second** time an opline runs: the first
time the temporary is empty, and afterwards it still holds the previous
iteration's reference, so the new one is written into the old box and the binding
keeps pointing at the previous iteration's data. `[&$r] = $arr` worked once and
then silently aliased the wrong array.

`ZEND_ASSIGN_REF` already drew this distinction between `store` and `bindSlot`;
the ref-producing fetches had not been told about it. Anything new that hands
out a reference needs the same treatment — and a differential case that runs it
**twice**, since one pass cannot see the difference.

## Destructuring by reference needs a real array

`[&$r] = $src` cannot shrug at a non-array the way the read form does:

| Source | Result |
|---|---|
| null / undefined | auto-vivified into an array |
| a string | `Error: Cannot create references to/from string offsets` |
| any other scalar | `Error: Cannot use a scalar value as an array` |

## The slot is made before the report, and read again after

A read that reports an undefined property on a write path creates the slot
*first*, so an error handler that writes the property writes into the slot the
operation is about to use:

```php
#[AllowDynamicProperties] class C { function error($_, $m) { $this->a = 12345; } }
set_error_handler([new C, 'error']);
$c->a %= 10;      // 5, not 0: the handler's 12345 is what the operation sees
```

Two things follow: writing null over the slot *after* the report undoes the
handler's write, and the read half has to be taken again once the report has
been handed out.

Not everything in this family is out of reach. A handler that resizes the
container the opcode is walking still is; a handler that writes one property is
just an ordering question.

## A warning can beat the refusal that would have followed

An error handler's `throw` is parked on the context and picked up at the top of
the interpreter loop, so an opcode that raises a *warning* and then refuses in
its own words has to take the pending error in between:

```php
set_error_handler(function ($s, $m) { throw new Exception($m); });
$x = "foo"; $y = &$x["2bar"];
// Exception: Illegal string offset "2bar"
// not: Error: Cannot create references to/from string offsets
```

`"2bar"` is a usable offset PHP disapproves of. The warning goes out, the
handler throws, and the refusal never happens.

## `@` narrows the level, it does not replace it

```php
error_reporting(E_WARNING);  @error_reporting();  // 0
error_reporting(E_ALL);      @error_reporting();  // 4437
```

The value `@` installs is `current & (E_ERROR|E_PARSE|E_CORE_ERROR|
E_COMPILE_ERROR|E_USER_ERROR|E_RECOVERABLE_ERROR)`, not that mask outright.

## At global scope a compiled variable is never undef

`runGlobal` binds every compiled variable to its entry in the globals table, so
a name the program only *mentions* is a reference holding undef rather than
undef itself. Anything asking "is this variable defined?" has to `Deref()`
first:

```php
foreach (get_defined_vars() as $key => $value) { ... }
```

`$value` does not exist yet when `get_defined_vars()` runs, but its slot is a
live reference -- and php2go looped twice over a one-entry array until the check
dereferenced.

## A variable name is an arbitrary byte string

PHP variable names are not limited to source identifiers. Computed names can
contain spaces, newlines, quotes, NULs or be empty, and the textual IR must give
such a place an unambiguous boundary. The renderer uses `${"..."}` with quoted
bytes and the scanner decodes that form before blanking string literals. The
ordinary `$identifier` form remains for source identifiers.

The spelling and the storage name are deliberately separate: diagnostics quote
the original PHP name, while Go locals use a mangled identifier. A test must
check that the mangled slot is declared as well as referenced; finding its name
in emitted text alone also passes when Go compilation later fails.

Computed `dynvar` fetches use a name-to-slot map. In `{main}` that map is the
global symbol table; in a function it maps names to the function's own slots.
The FETCH and ISSET/UNSET families encode which table to use in the opcode or
`extended_value`; the operand's shape cannot distinguish `$GLOBALS["x"]` from a
local dynamic name.

## Superglobals exist because the compile said so

Under the linked engine (embed SAPI, `auto_globals_jit=1`, variables_order
`EGPCS`), measured with `php2eval`:

| name | exists | holds |
|---|---|---|
| `$_GET`, `$_POST`, `$_COOKIE`, `$_FILES` | always, and first in the global table | empty arrays |
| `$_SERVER`, `$_ENV`, `$_REQUEST` | only if the file **spells** one, created in the order the compiler met them | the environment; `$_SERVER` adds `REQUEST_TIME_FLOAT`, `REQUEST_TIME`, `argv` (`[]`), `argc` (0); `$_REQUEST` empty |

No `PHP_SELF`, `SCRIPT_NAME` or `$argv`/`$argc` globals under embed.

`$GLOBALS["_SERVER"]` and `$$n` compile to **the same `FETCH_R`** as
`$_SERVER` -- identical opcode, operands and extended_value -- and do not
create it. So the op_arrays cannot answer "which superglobals exist"; the
compile can. The harvester re-arms the three and drops them from
`EG(symbol_table)` before each compile and reads back what the compile created
(`Unit.auto_globals`).

**Userland defines them, not Go** (the user's decision: most modern programs
read the request through a framework object, so what the superglobals hold is
the embedding's business). The bundled library's initializer
`register_superglobals_impl` (`internal/userland/ext/standard.php`) defines
the four always-present ones and whichever just-in-time ones
`PHP2Go\Internal\auto_globals()` lists, with values from `getenv()` and the
clock leaf (`auto_global_impl`). Go keeps two leaves in `runtime/stdlib`:
`auto_globals` (the harvested list, via `Ctx.SetAutoGlobals`) and
`register_superglobal`, which stores through the back-end's
`Ctx.SetSuperglobalDefiner` -- the interpreter's global table, or the compiled
program's `globals` map.

- **First definition wins**, once per request. A host or application
  initializer that runs before the library keeps its values; a file included
  after `unset($_SERVER)` does not bring it back.
- An included file naming a just-in-time superglobal first gets it when it
  runs (the interpreter asks `auto_global_impl` again), as the engine creates
  it at that file's compile.
- A registered name the engine does not define -- `$_RESP_HEADERS` -- is
  php2go's own state and stays out of `$GLOBALS` and `get_defined_vars()`.
- Only where the library is installed do superglobals exist: bundles, compiled
  programs (which now link and run the initializers), the interpreter's unit
  tests. `php2phpt` and `php2eval`'s interpreter arm install no library.

Until 2026-09-27 neither back-end created any of them, and a read warned
`Undefined global variable $_GET` and answered null -- a confident wrong
answer, invisible in the corpus (seven tests touch them) and loud in
`micro_bench`, where the warnings were most of `$x = $_GET`'s 3.9 s.
