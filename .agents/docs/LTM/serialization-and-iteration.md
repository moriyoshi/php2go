# Serialization and Iteration

## Summary

Two families whose behaviour is mostly format and protocol rather than engine
internals, and which therefore *can* be implemented from the specification —
but each has a detail that only the engine reveals. `serialize` encodes a
property's visibility in the name it writes; the iteration protocol's call order
is observable from the object being iterated.

## Key facts

| Thing | Detail |
|---|---|
| `serialize` property names | Mangled: `"\0*\0name"` protected, `"\0Class\0name"` private. That is what makes an unserialized private property land back in the right class |
| `serialize` object header | `O:<len>:"<Class>":<count>:{` — the class name ends with `:`, where a plain string value ends with `;` |
| `serialize` string bodies | Raw. The length delimits them, so a quote or a NUL needs no escaping and must not get any |
| `unserialize` failure offset | Where the innermost unreadable value *started*, not where the scan stopped |
| `var_dump` property labels | `["b":protected]`, `["c":"C":private]`; print_r uses `[b:protected]`, `[c:C:private]` |
| `Iterator` call order | rewind once, then next before every element after the first |
| `IteratorAggregate` | `getIterator()` may return a generator, an array, or another aggregate |
| `Generator::throw` | Raises at the yield the generator is suspended on, not at the call site |

## Details

### unserialize reports the innermost offset

Rewinding the reader on failure gets a top-level value right and a nested one
wrong: the outer container unwinds over the inner offset and reports its own
start. The offset is therefore recorded *once*, at the first failure, and left
alone as the failure propagates.

### One iterator abstraction, three users

`newObjectIterator` serves `foreach`, `yield from` and `f(...$traversable)`.
An aggregate recurses until something concrete comes back; an `Iterator` is
driven by ordinary `invoke` calls, so pulling an element can run arbitrary user
code and can throw.

`FE_RESET` has to decide whether the loop body runs at all, which for an object
means pulling the first element before the loop starts. The iterator carries a
one-element lookahead so `FE_FETCH` hands that element back instead of pulling a
second time.

### Generator::throw

The exception is parked on the generator and raised by `advance` through the
suspended frame — `frame.raise(err, pc-1)`, since `pc` is the opcode *after* the
yield. If no handler in the body catches it the generator finishes and the
exception escapes to the caller of `throw()`. Throwing into a generator that has
already finished simply throws at the call site.

### The internal array pointer

`current`/`key`/`next`/`prev`/`reset`/`end` share one position on `php.Array`,
and it indexes **buckets** rather than counting live elements — an element unset
behind it must not shift what it points at. Two rules are easy to get wrong and
both are observable:

- Once the pointer runs off either end it stays there. Only `reset()` and
  `end()`, which place it absolutely, bring it back; `prev()` from past the end
  answers false and does not step back into range.
- `key()` answers `null` where the others answer `false`, because `false` is a
  perfectly good array key.

The out-of-range check has to come *before* the skip-deleted loop: the pointer
parks at -1 after running off the front, and the loop would index that.

### Engine classes the runtime implements

`ArrayIterator` and `ArrayObject` have no harvested bodies, so their behaviour is
supplied in Go the way `Generator`'s and `Closure`'s is. One `arrayStore` covers
both — they differ only in that `ArrayIterator` *is* the iterator while
`ArrayObject` hands out a fresh one from `getIterator`.

Adding such a class means teaching **four** dispatch points about it, each of
which either has a whitelist or calls `resolveMethod` and finds nothing:
instantiation (`ZEND_NEW`), method calls (`ZEND_INIT_METHOD_CALL`),
`ArrayAccess` (`offsetCall`), and `newObjectIterator`. `count()` is a fifth:
a `Countable` counts itself, which the ctx-only builtin cannot ask it to do, so
both the specialized `ZEND_COUNT` opcode and the call path route through
`countOf`.

### What is deliberately not modelled

`WeakMap` and `WeakReference` are observable only through *when* entries
disappear, and Go's collector is neither refcounted nor deterministic. Modelling
them with strong references would turn a gap into a wrong answer, which is the
one thing the metric must not allow. See
[unsupported-versus-php-errors](./unsupported-versus-php-errors.md).

`ArrayIterator` and `ArrayObject` are engine classes with no harvested bodies,
so the user-`Iterator` path does not reach them; they need Go implementations
like `Generator`'s.

## Files

- `runtime/interp/serialize.go` — `serialize`, `unserialize`, the mangling and
  the reader.
- `runtime/interp/json.go` — `json_encode`, whose list-versus-object rule is the
  same key question in a different format.
- `runtime/interp/interp.go` — `iterator`, `iterNext`, `newObjectIterator`,
  `newDelegate`, and the `FE_RESET` lookahead.
- `runtime/interp/generator.go` — `generatorMethod`, including `throw`.
- `runtime/interp/arrayobject.go` — `arrayStore`, `countOf`.
- `runtime/php/array.go` — `Pointed`, `MovePointer`, `RewindPointer`,
  `EndPointer`.
- `runtime/interp/builtins.go` — `varDump` and `printR` property labels.

## Test coverage

`runtime/interp/differential_test.go`, in the blocks headed "serialize",
"json_encode", "A Traversable drives foreach" and "Generator::throw".

## Pitfalls

- `%q` is the wrong way to write any counted string: it applies Go's escaping to
  bytes the format says are raw.
- A property label depends on the *declaration*, which `var_dump` cannot look up
  — it is a plain builtin with no unit. The information rides on the object
  (`php.Object.Declared`), which is why that map is populated for every declared
  property and not only the typed ones.

## WeakMap

Implemented as a map; the *weakness* is not. php2go cannot know when an object's
last holder goes away, so nothing is ever dropped and `WeakReference::get()`
keeps answering. That is wrong for `weakmap_weakness` and the `gh10043-*` family
and right for the larger share of the directory, which uses a WeakMap as an
object-keyed map.

| Situation | Result |
|---|---|
| a non-object subscript — get, set, isset or unset | `TypeError: WeakMap key must be an object` |
| `$map[] = $v` | `Error: Cannot append to WeakMap` |
| reading an absent key | `Error: Object stdClass#2 not contained in WeakMap` |
| `var_dump` | a list of `["key" => …, "value" => …]` pairs, through the debug-info path |

`foreach` over one yields **object keys**, which `iterator.iterNext` cannot
express — it returns a `php.Key`. Recorded in TODO.md.

## Two dumper rules found with it

- `count()` must consult **Countable** for engine classes too, not only for user
  op_arrays and ArrayObject — otherwise `count($weakMap)` answers 1, the
  fallback for "not an array".
- An **integer key** in `__debugInfo` output prints unquoted: `[0]`, not
  `["0"]`. Real properties are always named, so the dumper quoted everything;
  what a debug-info handler returns is an ordinary array.

## foreach over an object

The object is flattened to an array of its **visible** properties, and three
rules apply that a plain array does not have:

- **Uninitialized typed properties are skipped**, not yielded as null.
- A **by-reference** loop binds the object's own slots, not the flattened copy —
  otherwise writing through the loop variable reaches nothing.
- A **readonly** property cannot be aliased at all:
  `Cannot acquire reference to readonly property C::$p`. Initialized or not.

`readonly_props/by_ref_foreach` needs all three: its first loop runs before the
property is initialized and must *not* raise, which only works if uninitialized
properties are skipped.

## What getIterator() may return

`zend_user_it_get_new_iterator` accepts the result only if it is an object whose
class has a `get_iterator` handler -- and refuses the aggregate *itself*:

```c
if (!ce_it || !ce_it->get_iterator ||
    (ce_it->get_iterator == zend_user_it_get_new_iterator && Z_OBJ(iterator) == Z_OBJ_P(object)))
```

So an **array is refused**, in the same words as a plain object:
`Objects returned by C::getIterator() must be traversable or implement interface
Iterator`, thrown as a plain `Exception`, naming the class the loop was written
against. `return $this` is refused by the identity half; returning a *different*
IteratorAggregate is fine and is unwrapped again.

Accepting the array is the tempting mistake -- it makes the loop work -- and so
is checking only Traversable, which lets `return $this` recurse forever.

## A by-reference foreach is bound to the variable

`foreach ($a as &$v)` does not iterate the array `$a` held when the loop
started. The engine makes `$a` a reference and reads it again on every turn, so
the loop follows whatever the variable holds now:

| in the body | what happens |
|---|---|
| `$a = null`, `$a = "s"` | `Warning: foreach() argument must be of type array\|object, %s given` and the loop ends |
| `unset($a)` | nothing: the reference outlives the name, so the walk continues |
| `$a = [9, 8]` | the loop follows the new array -- and never terminates; the engine dies of memory exhaustion |
| by-value loop | none of the above; it iterates a copy |

php2go records the compiled variable in the iterator (`iterator.source`) and
re-reads it before each `FE_FETCH_RW`. Undef is treated as "still going",
because that is what `unset` leaves behind.

## Which iterator accessors run is observable

A user `Iterator` can tell what the caller asked for, so php2go only calls what
it uses:

| Call | Accessors |
|---|---|
| `foreach` | rewind, valid, current, key, next |
| `iterator_to_array($it)` | the same |
| `iterator_to_array($it, false)` | no `key()` — the keys are discarded |
| `iterator_count($it)` | neither `current()` nor `key()` |

`iterator.skipCurrent` / `skipKey` carry that. A value-only comparison passes all
four regardless, which is why the differential case echoes from each accessor.

## The append slot and negative keys

Since PHP 8.3 the **first** integer key sets the append slot whatever its sign:

```php
$a = [-5 => "a"]; $a[] = "b";        // -4   (was 0 before 8.3)
$b = [-5 => "a", -9 => "b"]; $b[] = "c";  // -4 — the slot only rises
$c = ["x" => 1]; $c[] = "y";         // 0    — no integer key yet
```

`php.Array` carries `seenInt` for exactly this: without it the "only ever rises"
rule keeps the slot at 0 for any array whose integer keys are all negative.

Everything else about key coercion — float truncation, numeric strings, `"01"`
staying a string, `"9223372036854775808"` staying a string, bool and null —
matched the engine across a 24-shape matrix. So did all six sort functions and
`usort`. Recorded as a negative result: there is nothing to find there.

## `yield from` proxies, it does not copy

A generator suspended inside `yield from` has **no current value of its own**.
`current()` and `key()` read the inner generator *live*, however deep the chain:

```php
$src = upto(9);
$a = passthru($src);
$b = passthru($src);   // same source
// advancing $a moves $src, and $b must see where $src is now
```

Storing what was pulled in the outer generator's `frame.yieldValue` makes
`current()` a snapshot, and two delegates over one source then repeat each
other's values. `generators/yield_from_multi_tree` builds exactly that shape.

Walk the chain **iteratively with a hop limit** (`innermost()`), not
recursively: a delegation cycle is something PHP refuses to build but nothing
here prevents, and the recursive version overflows the stack rather than
answering oddly.

## `yield from` has three refusals

Checked before any draining starts, and they are three different errors:

| when | outcome |
|---|---|
| the delegate's frame is gone (`execute_data == NULL`) | `Error: Generator passed to yield from was aborted without proper return and is unable to continue` |
| the delegate is the generator currently being run | `Error: Impossible to yield from the Generator being currently run` |
| the delegate aborts *during* the delegation | `ClosedGeneratorException: Generator yielded from aborted, no return value available` |

"Frame is gone" is **not** "failed": a generator that returned normally has no
frame either and earns the same Error, because the engine tests
`execute_data == NULL` before it looks at `retval`. Reaching for a `failed` flag
gets this wrong in the quiet direction -- without the check at all, `yield from`
a dead generator just produces nothing.

## An overloaded container is not the same as an ArrayAccess

*Indirect modification of overloaded element of X has no effect* is about the
**dimension handler**, not about the interface. Three kinds:

| Container | `$c[$k][] = 1`, `$c[$k]++`, `$c[$k] = &$x` |
|---|---|
| Userland `ArrayAccess` | goes through `offsetGet`, gets a copy, earns the notice |
| Engine array store -- `ArrayObject`, `ArrayIterator` | the handler answers with the storage slot; all three really modify it, no notice |
| `WeakMap` | the same; the entry keeps a box bound by `=&`, so a later `$x++` is visible through the map |
| A subclass of an engine store that writes its own `offsetGet` | the handler calls the override, so it is overloaded again |

Two things follow for php2go:

- The write-path fetch has to hand out the real slot *and* note a write target,
  or `=&` writes through the box instead of replacing the entry.
- Only an explicit `&` marks the box shared. An indirect modification boxes the
  slot for its own reasons, and var_dump must not print `&` for it.

## Copying a property table keeps its references

`get_object_vars`, `get_mangled_object_vars` and `(array) $o` all copy the
table, and copying an array keeps a box two names share as a shared box:

```php
class R { public $a = 1; }
$o = new R; $ref = &$o->a;
$copy = (array) $o;   // ["a"] => &int(1)
$copy["a"] = 99;      // $o->a and $ref are 99 too
```

So these enumerations have to read the table with `EachRaw` and `CopyElement`,
not with `Each` -- which dereferences and flattens the box. It is the same rule
`array_values()` over an array a foreach left a reference in already follows.

## Unserialization uses ordinary property-write policy

Hydrating an object is not permission to write directly into its property
table. After class instantiability is checked, `unserialize` must use the same
property policy as an ordinary write: enums are refused before a property is
read, readonly and `NO_DYNAMIC_PROPERTIES` classes throw when an unknown
property would be created, ordinary classes emit the dynamic-property
deprecation, `#[AllowDynamicProperties]` suppresses it, and a declared property
does not trigger it.

The serialized mangled name still selects the correct declared private or
protected slot. The lesson is about the write path, not name normalization:
bypassing the ordinary object operation also bypasses its visibility,
readonly, dynamic-property and diagnostic rules.
