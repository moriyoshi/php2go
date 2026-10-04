# The Class Model and How It Is Harvested

## Summary

A `zend_class_entry`'s tables are *flattened*: `ce->function_table` holds every
method the class inherited as well as its own, and `ce->properties_info` holds
inherited properties too, keyed by name in no useful order. Harvesting them
wholesale looks right and is wrong in ways that surface far from the harvester —
in `self::`, in `var_dump`, in `foreach` over an object.

## Key facts

- **Methods:** filter on `fn->common.scope == ce`. Trait methods keep the
  *trait* as their scope, so they are filtered out too, and `resolveMethod` must
  consult `decl.GetTraits()` — ranking traits above the parent, which is the
  precedence PHP applies when flattening.
- **Properties:** sort by `info->offset`, which the engine hands out in
  declaration order with a parent's first. `properties_info` iteration order is
  not declaration order.
- **`$this` is an unused operand.** `$this->m()`, `$this->p++`, `$this->p[] = x`,
  `$this->p .= "y"`, `isset($this->p)`, `unset($this->p)` and `$this->p = &$x`
  all encode it as `op1_type == IS_UNUSED`.
- **Late static binding needs two classes per frame:** the declaring `class` for
  `self::`, and the `called` class for `static::`.
- **Modifiers are not in the op_array.** `visibility`, `static`, `abstract` and
  `final` are harvested from `fn_flags` in the cgo shim, not masked in Go.
- **Typed properties have a third state.** A declared type with no initializer
  is *uninitialized*, which is neither null nor absent.

## Details

### Why the method filter matters

Without `common.scope == ce`, a subclass carries its own copy of a parent's
method, so the resolved declaring scope is the subclass and `self::` inside an
inherited method names the wrong class.

### Why the property order matters

The harvester sorted by *name* for a while. That made every `var_dump` of a
two-property object wrong, along with `foreach` over an object and
`get_object_vars` — three visible symptoms of one invisible cause.

Two further things `property_info` does not tell you directly:

- **Names are mangled** for non-public properties — protected becomes
  `"\0*\0name"`, private `"\0ClassName\0name"`. Unmangle on harvest
  (`zend_unmangle_property_name_ex`); callers want the declared name, not the
  storage form.
- **Defaults are not on the info.** They live in the class's defaults table,
  indexed by the property's offset — `default_properties_table`, or
  `default_static_members_table` for statics. Without them every declared
  property initializes to null instead of what the source said.
- **`info->ce` is the declaring class**, which the entry alone does not say. It
  matters for statics: a subclass shares its parent's storage unless it
  redeclares the property.

### Why `$this` being unused matters

Reading op1 with the ordinary fetch yields undef, so every one of the opcodes
above reported "on null". `$this->f()` inside a method was broken across the
board — a large, quiet correctness hole that only surfaced once the error paths
started printing real messages. All of them now go through
`frame.objectOperand`, which falls back to `f.this`.

### Late static binding

`pendingCall` and `frame` carry a `called` class distinct from the declaring
`class`, so `static::` sees the subclass the call site named while `self::` sees
the body's own class.

### Method modifiers, and what depended on them

For a long time `Class.methods` was `repeated OpArray` with no flags, so nothing
downstream could tell a static method from an instance one. That single gap was
behind a family of unrelated-looking wrongness: `self::s()` from an instance
method kept `$this` bound, so a trace said `A->s` where PHP says `A::s`; no
visibility could be enforced anywhere; `Cannot call abstract method` could not
be raised at all.

The booleans are decided in the shim (`php2go_fn_is_static`,
`php2go_fn_visibility` and friends) rather than by masking `fn_flags` in Go. The
`ZEND_ACC_*` macros are in scope on the C side, and a shifted constant
transcribed into another language is exactly the kind of thing that goes quietly
wrong — see [zend-operand-encoding](./zend-operand-encoding.md) for the two
`*_MASK` fields that already did.

Visibility is decided against the **declaring** class, not the one the call site
named: private is visible only inside the class that wrote the method,
protected anywhere in the same hierarchy in either direction. An inaccessible
method falls through to `__call` / `__callStatic` before erroring — PHP treats
"you may not call this" and "this does not exist" identically at that point. The
message names the caller's scope: `from scope E`, or `from global scope`.

### Property visibility, and why it is not the method rule

Properties are decided against the declaring class like methods, but the
outcomes differ in a way the method rule has no equivalent for:

- A private property of an **ancestor** is not merely out of reach, it is
  *invisible*. The diagnostic is `Warning: Undefined property: C::$p`, and
  writing the name creates a dynamic property beside it. Only a private property
  of the object's **own** class produces `Cannot access private property`.
- Protected always produces the error, and it names the *object's* class rather
  than the class that declared the property.
- `__get` / `__set` / `__unset` answer for a property the caller may not reach,
  exactly as they do for one that does not exist. The error is what happens when
  the class has no accessor.
- `isset()` never raises: an unreachable property simply reads as not set.

`readonly` refuses a second write from every scope, including the declaring one,
and the "already initialized" check comes first — the message stops mentioning
visibility once visibility is no longer the reason. `unset()` on one is refused
outright.

Creating an undeclared property is deprecated since PHP 8.2 unless the class
opted back in, and the opt-in is a class *flag*
(`ZEND_ACC_ALLOW_DYNAMIC_PROPERTIES`), not an attribute to re-read.

That deprecation exposed a gap in the object model: PHP keeps a private property
and a dynamic property of the same name in **separate** slots, because the
declared one is mangled per class. One map holds both here, so which is which is
recorded on the object (`php.Object.Dynamic`).

### Uninitialized typed properties

A typed property with no initializer starts uninitialized. Modelled as
`php.Undef` in the property table, with the declaration recorded on the object
(`php.Object.Declared`) because the diagnostics need both the type and the
declaring class. Three observable consequences:

- `var_dump` lists it as `uninitialized(?string)` but does **not** count it in
  the object's property count.
- `isset()` is false *without* consulting `__isset`. The property exists; it
  merely holds nothing. Only `unset()`, which removes it outright, reaches the
  magic accessor.
- Reading it is `Error: Typed property B::$p must not be accessed before
  initialization`, naming the declaring class rather than the instance's.

### Magic accessors

`__get`, `__set`, `__isset`, `__unset`, `__call`, `__callStatic`, with a
re-entry guard so `return $this->$name` inside `__get` does not loop. `isset()`
is answered by `__isset` alone while `empty()` goes on to ask `__get` —
conflating them is observable.

### Autoloading

`spl_autoload_register` and friends; `resolveClass` consults the callbacks from
`new`, from static calls, and from class-constant fetches.

## Files

- `internal/zend/zend.go` — class harvest: the `common.scope` filter, the
  offset-ordered property walk, name unmangling.
- `runtime/interp/class.go` — `resolveMethod`, trait precedence,
  `isBuiltinOnly`.
- `runtime/interp/call.go` — `pendingCall`, `frame`, the `called` class.
- `runtime/interp/magic.go`, `runtime/interp/autoload.go`.
- `runtime/interp/interp.go` — `frame.objectOperand` and its call sites.

## Test coverage

`runtime/interp/differential_test.go`, with `internal/zend/testdata/classes.php`
as a harvest input.

## `readonly class` cannot be inferred from the properties

A readonly class makes every property it **declares** readonly, so the
flattened property list says nothing about the class itself -- and the one
property that matters for the link check, a trait's, is precisely the one that
is *not* readonly. `Class.readonly` is harvested from `ZEND_ACC_READONLY_CLASS`
for that reason.

`zend_do_traits_property_binding` refuses a non-readonly trait property with no
exception for a **static** one, even though a readonly class may not declare a
static property at all (`Static property Foo::$bar cannot be readonly`). The two
rules look like they should agree and do not.


## Pitfalls

- Class constants whose initializer the compiler defers — a spread of another
  constant, for instance — have no harvested value and currently report as
  undefined. Recorded in [TODO.md](../TODO.md).
- An engine class the runtime does not model must not be instantiable just
  because it was harvested; see
  [unsupported-versus-php-errors](./unsupported-versus-php-errors.md).
- The `IS_UNUSED` encoding is one instance of a wider family; see
  [zend-operand-encoding](./zend-operand-encoding.md).

## Trait members, and members the engine supplies

Two things a class has that the harvest cannot carry, both added 2026-08-23:

- **Trait properties and constants.** A class that uses a trait is not linked
  when the file compiles, so the engine has copied nothing into its tables.
  `Unit.properties` and `Unit.constants` fold them in on first use, recursing
  through traits that use traits, and rewrite the declaring class to the *using*
  class -- which is what `var_dump` prints for a private one and which class's
  static storage a trait's static property lands in. Methods deliberately work
  the other way: `resolveMethod` consults the trait so `self::` inside a trait
  method names the using class.
- **Engine methods.** `php2go_collect_methods` skips anything that is not a
  `ZEND_USER_FUNCTION`, correctly -- there is no op_array. But `method_exists`,
  `get_class_methods` and `is_callable` ask about the linked build, not about
  what this runtime implements, so `Class.engine_methods` carries their names,
  visibility, staticness and abstractness. Calling one that is not modelled
  still reports as a gap.

Rules that are not what they look like:

| Rule | Note |
|---|---|
| `Trait::K` is an Error, not a missing constant | `Cannot access trait constant T::K directly`; a class using the trait sees it fine |
| `defined('Trait::K')` is false, not a throw | `defined()` fetches silently; a runtime gap still propagates |
| `Closure::__invoke` is not in Closure's function table | `method_exists` has an explicit branch for it; the engine answers calls with a trampoline |
| `method_exists` answers two different questions | Asked about an **object** it ignores visibility entirely; asked about a class **name** it excludes a private method the class inherited rather than declared (`Z_TYPE == IS_OBJECT \|\| !(flags & ZEND_ACC_PRIVATE) \|\| scope == ce`). This is *not* `is_callable`'s rule — that one runs `zend_check_protected` against the calling scope |
| `instanceof` must compare class *entries*, not names | `class_alias` shares one entry under two names |

## Destructors

PHP destroys an object the moment its last holder goes away. php2go has no
refcounts, so it reproduces the half that does not need them: the end of the
request. `Unit.live` remembers every object whose class resolves a `__destruct`
body, and `RunUnit` runs the two steps of `shutdown_destructors` after the
registered shutdown functions —  globals in **reverse** insertion order, then
whatever objects remain in creation order.

An object PHP destroyed mid-script is destroyed here instead: late rather than
never. Tests that assert on *where* the output appears still fail, and one
(`bug73338`) fails because a destructor that throws is no longer inside the
`catch` that PHP had on the stack.

**PHP does not destruct what it did not construct.** Both of these leave an
object that is never destructed:

```php
new Boom;             // the constructor throws
new Dt(bang());       // an argument throws, so the constructor is never entered
```

Modelled by blocking destruction at `ZEND_NEW` whenever a constructor is going
to run (`pendingCall.ctorObject`) and releasing it when that call *returns*.
Marking on the error path instead only covers the first of the two.

## Linking asks for classes

`ZEND_DECLARE_CLASS` and `ZEND_DECLARE_ANON_CLASS` ask for the parent, every
interface and every trait (`resolveDependencies`), and a variance check asks for
the classes its types name. This is where the engine discovers it has not seen a
class, and where the autoloaders run — an autoloader can print, so *whether* and
*in what order* php2go asks is directly observable.

| Predicate | Answers | Right for |
|---|---|---|
| `findClass` | is it in the harvest | reading a declaration |
| `classDeclared` | has its opline run — **true** for a name it has never heard of | `class_exists` |
| `classAvailable` | both | linking, `new`, `FETCH_CLASS` |

The distinction is not academic. The harvester walks closures, so a class
declared *inside an autoloader* is in the harvest from the start; `findClass`
finds it and the autoloader never runs. That single confusion was the whole of
`type_declarations/variance/class_order_autoload*`.

`resolveClass` autoloads first and falls back to the harvested declaration if
nothing produces the class. Refusing outright would be a bigger claim — that a
program may not reach its own class — than the evidence supports.

A dependency that stays missing is a **catchable** `Error`, not a compile-time
refusal: `Class "A" not found`, `Interface "I" not found`, `Trait "T" not
found`, raised through `f.raise` so a `try` around the declaration sees it. Note
an autoloader is a call and a call moves the reported location — it has to be
restored before raising, or the diagnostic names the autoloader's line.

## A class name caches its class entry, and the cache outlives the class

An interned class name carries a `map_ptr` slot holding the last class entry
looked up under it, and **`zend_lookup_class_ex` answers from that slot before
it consults the class table**. Removing a class from `CG(class_table)` therefore
does not make it unreachable: the name still resolves, to freed memory. In the
harvester that is a use-after-free the engine walks straight into — a child
class whose parent was declared by an earlier file reaches
`zend_can_early_bind` with a dangling `parent_ce`, iterates its
`function_table`, and segfaults in `zend_hash_find_known_hash`.

`php2go_drop_user_ce_caches` is the cleanup, and both `php2go_forget_classes`
and `php2go_reset_user_symbols` call it *before* removing anything. Clearing a
slot is always safe: it is a cache, and a miss falls back to the table.

Three facts that make this easy to get wrong:

- **The slot is keyed on the string used at the *use* site, not on `ce->name`.**
  `zend_lookup_class_ex` fills it from whatever name it was called with, so a
  name written in a different case has its own slot. Clearing `ce->name` alone
  is not enough; every slot lives on an interned string, so
  `CG(interned_strings)` is the complete set to walk.
- **The slot is filled by inheritance linking only.** `zend_try_early_bind` and
  `zend_do_link_class` set it; the `zend_hash_add_ptr` branch that a parentless
  class takes in `zend_compile_class_decl` does not. So a class with no parent
  never has a stale slot, and every synthetic reproducer built from one is
  quiet.
- **`zend_alloc_ce_cache` only ever touches interned strings**, and skips
  permanent ones outside module startup — so user classes are always on
  request-interned strings and internal ones on permanent strings.

This is not testable through behaviour. Whether the next compile early-binds
against the stale entry depends on what has been allocated over the freed
memory, so the same two files crash inside a corpus run and compile quietly on
their own — and asserting on the emitted opline passes either way (verified: the
child still emits `ZEND_DECLARE_CLASS` with the cleanup disabled). Assert the
pointer instead: `DanglingClassNameCaches` counts slots holding an entry the
class table no longer has, comparing pointers rather than dereferencing them,
which keeps it safe to call exactly when the invariant is broken.

## Attributes

The whole `#[...]` list travels in the IR for classes, methods and functions,
properties and class constants: name, arguments (positional and named), line.
Before 2026-08-24 only `#[\Deprecated]`'s two arguments were read.

The arguments stay **unevaluated**, as literals. That is the semantics, not an
optimisation: `#[Attr(SOME_CONST)]` is legal above a declaration that compiles
before the constant exists, because the engine keeps the argument as an AST and
evaluates it only when something reflects on it.

`zend_attribute` is variable-length — the arguments follow the struct inline —
so the table cannot be mirrored in Go and every field goes through a C shim
(`php2go_attr_count`, `php2go_attr_at`, `php2go_attr_arg_value`, …).

### Where the ReflectionClass tests are

**223 of the 267** blocked on `ReflectionClass` are `lazy_objects` —
`newLazyProxy`/`newLazyGhost` account for 363 call sites between them. Reflection
is not the feature those tests need. Budget accordingly: the rest is 19 in
`attributes`, 5 in `enum`, and a long tail.

### Reflection placement rules

- `new ReflectionClass("NoSuch")` throws **ReflectionException**, not the `Error`
  a missing class usually earns.
- `new Attribute(-1)` is **accepted**. A bad flags value is refused only where an
  attribute of that class is instantiated — which is also where an argument
  naming something undefined (`#[Attribute(Foo::BAR)]`) produces
  `Class "Foo" not found`.
- A reflection constructor needs the class table, which the `builtinCtor` hook
  cannot reach, so these classes route `__construct` through `builtinMethod`.

### The member reflectors

`ReflectionFunction`, `ReflectionMethod`, `ReflectionProperty` and
`ReflectionClassConstant` share one `reflector` struct, distinguished by which
field is set; `ReflectionNamedType` holds a type as *written*, so `?int` is the
name `int` that allows null. `ReflectionClass` hands them out through
`getMethod`/`getProperty`/`getReflectionConstant`/`getProperties`.

Details the corpus checks:

| Rule | Note |
|---|---|
| `ReflectionMethod("Class::method")` — the one-argument form — is **deprecated** in 8.4 | the notice is compared |
| Diagnostics from `newInstance()` name the line the **attribute was written on** | not the `newInstance()` call; the harvested attribute carries its line |
| The attribute constructor's **visibility** is checked | `Call to private A3::__construct() from global scope` |
| A class with no `#[Attribute]` | `Attempting to use non-attribute class "A5" as attribute` |
| `getParentClass()` on a class with no parent returns **false**, not null | — |

## `Class.interfaces` is the *linked* list

It includes what the engine added, not only what the source wrote, and nothing
distinguishes the two. Any check of the form "the source may not name interface
X" is therefore **not implementable from the harvest**:

`enum Foo: int implements BackedEnum {}` is a fatal in PHP, because the engine
implements `UnitEnum`/`BackedEnum` for every enum already. Implementing that
check against the harvested list refuses *every* enum — it took `Zend/tests/enum`
from 102 passing to 36 before the cause was obvious.

Checks that only look at what the source wrote need the syntactic list, which
would have to be harvested separately.

## Enum from() and tryFrom()

The declared parameter is **not** the backing type:

| Enum | Parameter |
|---|---|
| `enum E: int` | `int` |
| `enum E: string`, weak mode | `string\|int` |
| `enum E: string`, strict mode | `string` |

The `string|int` exists so passing an int does not look like a coercion (the
comment is in `zend_enum_from_base`), and it is observable as the type a
TypeError names. An int accepted that way is **rendered** with
`zend_long_to_str`, not coerced.

The argument check runs *before* the case table is built, so a wrong-typed call
reports that rather than a duplicate-value or case-type problem in the enum
itself.

## instanceof follows the whole graph

An interface has parents of its own: `class C implements Iterator` **is** a
`Traversable`. `Unit.instanceOf` checked only the class's direct interfaces plus
its parent chain until 2026-08-25, so every grandparent interface answered no.

It now walks `abstractSources` — parents, interfaces, transitively — **minus
traits**, since composing a trait is not a subtype relation and
`abstractSources` includes them for the abstract-method check.

Note there were two implementations of this question: `instanceOfName`, added
later for the signature checks, had always walked the graph properly. When a
second copy of a predicate appears, the older one is the one to suspect.

## A flattened table says where a thing was found, not where it was written

Zend flattens inherited members into every subclass's table. The copy carries a
`declaring_class`, and everything that depends on *where the member was written*
has to follow it back:

- **Visibility.** A private property of an ancestor is in the subclass's table;
  comparing the reading scope against the class whose table it was found in
  hides it from the class that declared it.
- **`self::` in a class constant's initializer.** The scope is the declaring
  class, whichever subclass the constant is read through.
- **A static property's initializer.** The flattened copy has no default at all.
  Building the shared storage from it gives null to a property the parent gave a
  value -- and because parent and child share one slot, the parent's value is
  lost too.

All three were found as separate bugs before the pattern was named. When
something depends on a declaration rather than on a lookup, check which of the
two the code is holding.

## The reflectors' __toString is one shared format

`Reflection*::__toString` is ext/reflection's `_class_string`, `_function_string`,
`_property_string`, `_class_const_string` and `_parameter_string` -- one format
that nests, not one method per class. php2go keeps it together in
`runtime/interp/reflectdump.go`.

The parts that are not guessable:

| Detail | Rule |
|---|---|
| Position line | `@@ file 11-17` for a class, `@@ file 8 - 8` for a method -- spaces on one, not the other |
| Empty section | Still prints its braces; `Static methods` and `Methods` write the newline *before* each entry, so an empty one needs one written explicitly |
| Parameters block | Printed whenever the declaration has an arg_info table, which it has as soon as there is a parameter **or a return type** |
| Inherited private members | In the tables, but excluded from the listing *and* the count |
| Method annotations | `inherits X` (body from elsewhere), `overwrites X` (replaced a visible one), `prototype X` (the interface or abstract declaration) -- independent, and two can appear together |
| Defaults | `format_default_value`: `'x'` in full, arrays expanded to `[1, 'k' => 2]`. Not the signature renderer, which cuts strings at ten bytes |

A **user** parameter's default is not on the parameter. It is in the `RECV_INIT`
opline that fills the slot (`get_default_from_recv`); `ArgInfo.default_value` is
only ever populated for internal declarations. Reading it from the wrong place
is silent -- the parameter simply prints without ` = ...`.

Reading the formatter's source is the right move here, and does not contradict
"ask the engine": a formatter is a specification of output, not an inference
about operand encoding. The oracle then confirms it -- the class form matched
byte for byte on the first run apart from the RECV_INIT defaults.

## An anonymous class's name is cut where the printf puts it

The real name is `class@anonymous\0<file>:<line>$<hash>`. Diagnostics are printf
over C strings, so it is cut at the NUL -- **per interpolated argument, not per
finished message**, which is what makes three outputs of the same class differ:

| how the message is built | what is printed |
|---|---|
| `"%s(): Argument #%d..."` with `"Class::method"` as one string | `class@anonymous(): Argument #1 ...` -- the `::method` is inside the cut string |
| `"%s%s%s(): Return value..."` with class, `"::"`, method separate | `class@anonymous::test(): Return value ...` |
| a declared type, each class name resolved on its own | `class@anonymous\|string` -- the rest of the union survives |

Cutting the finished message instead gets the first case right and the other two
wrong. `resolve_class_name` (zend_compile.c) states the reason for the type
case: cutting there avoids "larger parts of the type being omitted by printing
code later".

php2go: `php.CutAtNul` is the primitive, `functionLabel` cuts the joined label,
`functionLabelScoped` cuts only the class, and `displayType` cuts each atom.
