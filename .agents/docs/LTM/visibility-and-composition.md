# Visibility, Trait Composition and Closure Binding

## Summary

Three link-time and access-time rules that are easy to approximate and easy to
get subtly wrong. Each has a short, authoritative function in php-src, and in
every case reading that function settled a detail no amount of testing against
one example would have.

## Key facts

| Fact | Authority |
|---|---|
| Protected access is checked against the class that *first* declared the method, not the one the lookup found | `zend_check_protected` + `zend_get_function_root_class` |
| A private parent method is a different method, so the root walk stops there | same |
| `is_callable` is false for an inaccessible or abstract method — unless the matching magic forwarder exists | `zend_is_callable_ex` |
| Which forwarder applies depends on how the callable *named* its target: `__call` for an object, `__callStatic` for a class name | same |
| `$obj->staticMethod()` is legal and binds no `$this` | — |
| `isset(A::$priv)` answers false rather than raising; a read raises | — |
| Inheritance is applied **before** traits are bound | `zend_do_link_class`: `zend_do_inheritance` then `zend_do_bind_traits` |
| The first name in a trait conflict message is the composing class only when the collision is with its own member | `find_first_constant_definition` |
| A closure's scope argument defaults to the *string* `"static"`, meaning keep | `do_closure_bind` |
| The instance bound to a closure never sets its scope — it sets `called_scope` | same |

## Details

### Protected and the root class

`class A { static protected function ma() {} }`, `B1 extends A` overriding
`ma`, `B2 extends A` calling `is_callable('B1::ma')` → **true**, even though B2
is not a subclass of B1. The check runs against A, because B1::ma's prototype is
A::ma. Replace `A::ma` with a *private* `A::mp` and the answer flips to false:
a private method sets no prototype, so B1::mp's root is B1.

`Unit.methodRoot` walks up while an ancestor declares the same name
non-privately, and `methodAccessible` checks the hierarchy against that.

### is_callable

Accessible and not abstract → true. Otherwise the class is callable through
`__call` (object form) or `__callStatic` (class-name form) if it has one. Note
that the forwarder is chosen by the *syntax of the callable*, not by whether the
method is static: `is_callable(['E', 'f'])` with a private `E::f` and an
`__call` is false, because a class-name callable needs `__callStatic`.

### Trait composition happens at DECLARE_CLASS

php2go does not flatten traits into the composing class — `resolveMethod`
consults the traits directly so `self::` inside a trait method names the *using*
class. The engine's composition checks therefore have to be made explicitly, in
`runtime/interp/traits.go`, called from `ZEND_DECLARE_CLASS`.

The placement is observable: `traits/constant_005` prints `PRE-CLASS-GUARD`
before the class declaration and expects the fatal after it. A check made at
harvest time would print it first and fail the test.

Conflict rule: same name in two contributors conflicts unless the flags
(visibility + final for constants, visibility + static for properties) and the
values are all equal. Values that are deferred initializers are evaluated first.

Message order — `find_first_constant_definition` — decides between:

- `ComposingClass and TestTrait` — the class's own constant collided.
- `Trait1 and Trait2` — Trait1 contributed it first, then Trait2 collided.
- `BaseClass2 and TestTrait2 ... in the composition of DerivedClass2` — an
  *inherited* constant collided, which only happens because inheritance runs
  before trait binding.

### Fatal errors are not exceptions

`Interp.fatal` writes `"\nFatal error: <msg> in <file> on line <line>\n"` to the
script's output and returns an `exitTermination`. Nothing catches it, and
shutdown functions still run — the same shape `exit()` has.

### Closure binding

`do_closure_bind` in `zend_closures.c` is twenty lines and every one matters:

```
scope arg is an object  -> its class
scope arg is "static"   -> keep the closure's current scope   (this is the default)
scope arg is null       -> unscoped
scope arg is any string -> looked up; not found is a Warning and null
```

Then `zend_valid_closure_binding` refuses two binds outright, warning and
returning null: an instance onto a static closure, and unbinding `$this` from a
closure whose body uses `$this` (`ZEND_ACC_USES_THIS`, harvested onto
`OpArray.uses_this`).

The instance sets `called_scope` — what `static::` resolves to — and never the
scope. Deriving the scope from the object makes `bindTo(new A, "static")` grant
access to `A`'s privates, which PHP does not.

## Files

- `runtime/interp/class.go` — `methodAccessible`, `methodRoot`, `methodCallable`,
  `namedMethodCallable`, `staticPropAccess`.
- `runtime/interp/traits.go` — `composeTraits` and the conflict checks; `fatal`.
- `runtime/interp/closure.go` — `bindClosure`.
- `runtime/interp/program.go` — `is_callable`.
- `third_party/php-src/Zend/zend_inheritance.c`, `zend_closures.c`,
  `zend_API.c` (`zend_is_callable_ex`).

## Test coverage

`runtime/interp/differential_test.go` covers the visibility matrix from
`access_modifiers_009`, the abstract and magic-forwarder cases, static access
through an instance, static-property visibility, and the four closure-binding
rules. The trait conflicts are covered by the `.phpt` corpus rather than
differentially: their text names the file, and `php2eval` and the interpreter
disagree about what that file is called.


## What a closure is bound to

Two things are separate and both observable: the `$this` and the scope.

- A **`static function () {}` written inside a method captures no `$this`**,
  even though one is in scope. That is what makes `bindTo(null)` on it a no-op:
  the unbind warning needs `this_ptr` set *and* `ZEND_ACC_USES_THIS`, and a
  static closure whose body mentions `$this` still has the flag.
- A trace names the **scope**, not the declaring class, and there are three
  shapes:

  | binding | trace |
  |---|---|
  | as declared in a method | `A->{closure:A::make():2}()` |
  | `bindTo($obj, null)` -- `$this` kept, scope dropped | `Closure->{closure:A::make():2}()` |
  | `bindTo($obj, "B")` | `B->{closure:A::make():2}()` |
  | `Closure::bind($c, null, "B")` -- scope, no `$this` | `B::{closure:...}()` |
  | no `$this`, no scope | `{closure:FILE:LINE}()` |

  The `Closure->` case is the one that is easy to miss: with no class to name,
  the trace names the class the value actually is.

## Class constants have visibility, and `use T` moves it

`private const` and `protected const` are enforced exactly like a static
property's: private wants the scope to *be* the declaring class, protected wants
the two in one hierarchy in either direction. `Cannot access protected constant
A::FOO`.

The trap is the same one the private-method rule has: a trait's constant is a
member of the **using** class, not of the trait. Measuring against
`declaring_class` -- which names the trait -- makes a private trait constant
unreachable from the class that used it.

## Which link checks the harvest already makes

A check the engine performs at **compile** time never reaches php2go's linker:
the harvest fails and `php2phpt` reports `compile-error`. That is why
`class Q extends P { static function m() {} }` needed nothing, while
`interface a extends d, c` with the same disagreement needed a check -- an
interface list is linked when the class *binds*, not when it is compiled.

The interface diagnostic reads back-to-front, and that is the engine, not a
slip: `zend_do_implement_interfaces` adds interfaces in order and treats each
new one as the **parent** of what is already there. For `extends d, c` the
message is `Cannot make non static method c::B() static in class d` -- parent c,
the method under *d's* spelling, d as the class being changed.

## A private method is per-class

`zend_std_get_method` resolves against the object's class, and if what it finds
is private and belongs to some *other* class it looks again for a private method
of the **calling scope** with the same name:

```
class A { private function t() { echo "A::t\n"; } public function callIt() { $this->t(); } }
class B extends A { private function t() { echo "B::t\n"; } }
(new B)->callIt();   // A::t, not B::t and not an error
```

The second lookup must see what the scope's **traits** gave it as well as its
own table -- a private trait method used by both a parent and a child is the
same rule (`gh14009_005`). A scope that declares no such method gets the
ordinary refusal.

## An exception handler is cleared while it runs

`ZVAL_UNDEF(&EG(user_exception_handler))` before the call, so:

- a handler that throws and installed nothing has **no** handler -- the second
  exception is fatal, which is what "handlers are not called twice" means;
- a handler that installed one while running is answered by *that* one.

php2go keeps a stack rather than a single slot, so this is a null pushed on top
for the duration. The nested lookup must run **with that placeholder still in
place**: dropping it first exposes the original handler again and the two call
each other forever.

## `self` in a callable is resolved by the callable parser

Two separate facts, and they are easy to get backwards:

- **Where.** The keyword handling lives in the `callable` ZPP, not in the call
  opcode. `call_user_func(["self", "m"])` resolves and is deprecated;
  `$a = ["self", "m"]; $a();` is `Error: Class "self" not found`. Same for
  `parent`, `static`, and the `"self::m"` string spelling.
- **When.** The engine resolves the callable where it *validates* the argument.
  A callback stored for later -- `register_shutdown_function(["self", "m"])` --
  must therefore be settled at registration, since the scope is gone by the
  time it runs.
- **What else gets settled.** The *instance* too. `call_user_func` lends the
  caller's `$this` to a callable naming an instance method, and a stored one
  keeps it: `register_shutdown_function('Test::method')` written inside a Test
  method runs with that instance. php2go does both halves in
  `Unit.settleCallable`, storing the result as `[$object, "method"]`.

The oracle cannot be asked about the second directly: a
`register_shutdown_function` with a resolved scope segfaults the shared engine
during `php_embed_shutdown`. Probe up to registration and let the `.phpt` check
the rest.

## Pitfalls

- A clustering script over `-diff` output must implement the `EXPECTF`
  placeholder rules. Comparing literally groups tests by their first
  *placeholder* line and produces clusters that are not real.
- "Same hierarchy" is not the protected rule, and the difference only shows up
  with siblings.
- Approximating a link-time check at harvest time changes *where in the output*
  its diagnostic appears, which several tests assert on.

## Declaration-time checks at ZEND_DECLARE_CLASS

Everything the engine does in `zend_do_link_class` has to be redone here,
because a class with an interface or an unresolved parent is not linked when the
file compiles. `runtime/interp/traits.go` and `runtime/interp/abstract.go` hold
what is implemented; the placement matters, since several tests assert the
diagnostic appears *after* output the script already produced.

| Rule | Note |
|---|---|
| An abstract declaration is a requirement, not an implementation | `resolveMethod` must walk past one; a trait's abstract method ranks above the parent's concrete one but does not satisfy the call |
| A plain property satisfies an interface's hook requirement | `interface I { public $p { set; } }` is implemented by `public $p;` |
| An enum, or an already-abstract class, gets a different message | `must implement N abstract private method(s)` rather than `contains N abstract methods and must therefore be declared abstract` |
| The redeclaration message names the **first** declaration's kind | `interface A {} trait A {}` says "Cannot redeclare interface A"; the class table keeps only the last, so `Load` keeps the first too |
| Whether a DECLARE_CLASS is a redeclaration is a counting question | An unconditional declaration is bound at compile time with no opline, so `harvestCount > 1` means the single opline is already a redeclaration |
| `class_alias` onto a taken name is a Warning | …and names the kind of the class being *aliased*, not the one holding the name |

## Enum restrictions

Most are link-time refusals, checked at `ZEND_DECLARE_CLASS` with the trait and
abstract ones (`runtime/interp/abstract.go`). An enum implements `UnitEnum`, so
its declaration is always deferred — the harvest never sees these even though
the engine calls them `E_COMPILE_ERROR`.

| Rule | Kind |
|---|---|
| Only `__call`, `__callStatic`, `__invoke` are allowed; every other magic method is `Enum Foo cannot include magic method __X` | fatal |
| An enum may not declare `cases`, `from` or `tryFrom` — `Cannot redeclare Foo::cases()` | fatal |
| A non-enum may not implement `UnitEnum` or `BackedEnum` | fatal |
| A class may not extend a final one | fatal |
| Cloning a case: `Trying to clone an uncloneable object of class Foo` | catchable Error |
| A dynamic property on a case: `Cannot create dynamic property Foo::$baz` — an Error, not the deprecation other classes get | catchable Error |
| Two cases sharing a backing value: `Duplicate value in enum Foo for cases Bar and Baz` | catchable Error |

The last one is subtler than it looks. It is what building the **backed value
table** notices, so:

- reaching a case by name raises it, and so does `from()`/`tryFrom()`;
- `cases()` does **not** — it walks the declarations and needs no table;
- it raises *every* time, because the table never gets built. Memoise the
  verdict, not the fact of having checked.

## Signature compatibility

`zend_do_perform_implementation_check`, run at `ZEND_DECLARE_CLASS` alongside
the abstract and trait checks (`runtime/interp/signature.go`). 124 tests in
`Zend/tests` quote the message it produces.

| Rule | Note |
|---|---|
| Required argument count may not increase | optional ones may be added freely |
| A declared argument may not be removed | arity is checked against the declaration |
| Parameter types are **contravariant** | the override must accept everything the prototype did |
| Return types are **covariant**, and may not be removed | adding one where there was none is fine |
| A by-reference **return** is covariant, a by-reference **argument** is invariant | dropping `&` on a parameter is a refusal either way |
| A variadic may not become non-variadic | past the end of a variadic declaration, every further position is that parameter again |
| A private prototype constrains nothing — unless abstract | only a trait can have an abstract private method |
| A constructor is exempt | unless the prototype is an interface's or is abstract |

The verdict is a **pair**: compatible, and *decidable*. A type naming a class the
program has not declared, or an intersection/DNF type, leaves the relation
unknown, and an unknown relation must not refuse — inventing a fatal error the
program did not earn is worse than missing one. A full corpus run is the only
way to find these: the first version produced five spurious fatals.

Four rules found that way, each a real one:

- **`class_alias`** — both sides of a class comparison go through the class
  table, or an alias looks like a different class.
- **`never` is the bottom type** — `__toString(): never` satisfies `: string`.
- **A concrete trait method is not a prototype.** Composing a trait copies its
  methods in; a method the class writes itself *replaces* one rather than
  overriding it. Only an abstract trait method constrains.
- **`static` on the prototype side is only satisfied by `static`** — it is a
  promise about the called class, which `B::m(): B` would break for a grandchild.
  On the child side it resolves to the child.

### What the declaration is checked *for*

Not what the class declares — what it **contributes**. A trait's method is
checked against the parent's prototype, and the diagnostic names the trait
(`SayWorld::sayHello(Base $d) must be compatible with Base::sayHello(array $a)`),
because the engine copies a trait function in without rewriting its scope.

### Rendering

`zend_get_function_declaration` is half the feature — the rule can be right and
the test still fail. `& ` prefixes a by-reference return; each parameter is
`type &...$name`, and a default is rendered from the `RECV_INIT` opline carrying
it, not from the argument: `'0123456789...'` cut at ten bytes, `[]` or `[...]`
for an array, a bare constant name for `PHP_EOL`, `<expression>` otherwise.

A mismatch is reported at the **child method's** own file and line
(`fatalAt`), which is generally not where the class declaration is.

The printer takes a **scope** separately from the function, and
`zend_type_to_string_resolved` substitutes it for `self` and its parent for
`parent`. `static` is not substituted -- it stays `static`, being a promise
about the called class rather than a name. The scope is
`fixup_trait_scope(fn, ce)`: a trait's method keeps the trait for the `T::`
prefix but resolves its types against the class that composed it, so
`trait T { abstract private function m(self $x): self; }` used by `C` prints as
`T::m(C $x): C`. php2go: `renderSignatureIn(owner, scope, op)` and `selfScope`.

### Where a prototype comes from when a trait is involved

The engine flattens a trait's methods into the composing class's function table;
php2go deliberately does not (see [`resolveMethod`]), so anything that reads a
class's method table has to follow the composition itself:

- A **subclass** overriding a method its parent got from a trait is checked
  against that method, and the diagnostic names the *parent*, not the trait and
  not an interface further down the source list. `prototypeMethod` follows
  `composedTraitMethod` for this.
- An **abstract trait method** is checked against whatever the function table
  already holds, which may be a method the class *inherited*. `gh14009_002`
  reports `Declaration of P::common() must be compatible with T::common(int
  $param)` -- a pair neither of which the linking class wrote. That candidate is
  answerable only to the trait that requires it; checking it against everything
  else would re-report what its own class already settled.
- `use T { T::m as final; }` copies the method in **with the clause's
  modifiers**, so a subclass overriding it gets `Cannot override final method`.
  The alias's modifier word is not necessarily a visibility: only a PPP bit
  changes visibility, and `as final` alone must leave it alone.

## What an `as` clause changes, and for whom

`use T { m as final; m2 as protected; }` changes the method *as the composing
class holds it*. The trait's own declaration is untouched, so anything that
answers a question about the method has to answer for the **pair** (declaring
class, name), never for the op_array alone:

- `effectiveVisibility(declaring, method, name)` and `effectiveFinal(...)` are
  the two accessors; the call path, `methodAccessibleAs` and every reflection
  predicate go through them.
- Both follow `aliasFor`, which walks the **trait graph**, not just the class's
  own clauses: `trait T2 { use T1 { aa as public; } }` used by a class gives
  that class a public `aa`, and looking only at the class's own aliases misses
  it entirely.
- The clause's name is the one it *composes under* -- `m as final other` governs
  `other` -- which matters for the `__construct` exemption below.

`Private methods cannot be final as they are never overridden by other classes`
has two sites. The compiler emits it for a declaration that writes both, and the
harvest carries that as a compile diagnostic. Trait binding emits it again for
an alias that *adds* `final` to an already-private method
(`zend_traits_check_private_final_inheritance`) -- and only then: a method the
trait already declared final earns nothing when an alias makes it private, and
a constructor is exempt.

Related: the harvested `TraitAlias.modifiers` is a flag word. Only a PPP bit
decides visibility; mapping any non-zero value through a visibility helper turns
`as final` into `public`.

## Callables that name a scope

The method half of a callable may be compound: `[$obj, "parent::m"]`,
`["C", "self::m"]`. Two rules, both easy to get backwards:

- The scope is resolved against **the class the callable named** — the object's
  class — not against the calling function's scope. `zend_is_callable_check_func`
  sets `scope = ce_org` when there is one. So `[$this, "parent::func"]` inside
  `B::callFuncInParent` with a `C` instance calls **B**::func, not A::func.
- The form is deprecated, not refused:
  `Callables of the form ["C", "parent::func"] are deprecated`. The first name
  is the class *written down*; the second is the method string as written.

The bare `["parent", "m"]` form is different — that keyword resolves against the
calling scope and raises `Use of "parent" in callables is deprecated`. When the
compound form is used, that second deprecation is suppressed.

## Why a callback is invalid

`call_user_func` and friends validate the callback before calling anything and
name the reason (`callableReason`, raised at `ZEND_INIT_USER_CALL`):

| Reason | When |
|---|---|
| `class "X" not found` | the class half does not resolve — **after** autoloading |
| `class C does not have a method "m"` | resolved, no such method |
| `cannot access private method C::m()` | found but not accessible from the calling scope |
| `cannot access "self" when no class scope is active` | keyword used outside a class |
| `first array member is not a valid class name or object` | element 0 is neither |
| `array callback must have exactly two members` | wrong shape |

The class named is the one the callable **resolved to**, not the one that
declared the method: a private `A::func22` reached through `parent::` from a `C`
is reported as `B::func22`.

A plain dynamic call (`$f(...)`, `ZEND_INIT_DYNAMIC_CALL`) keeps the bare
`Error` — this wording belongs to the builtin, not to calling.

Resolving the class half **autoloads**, and that is observable:
`call_user_func_002` asserts on the autoloader printing the name it was asked
for, and on it *not* being asked for the empty string.

## `__call` versus `__callStatic`

Not "the instance one and the static one". `zend_std_get_static_method` prefers
**`__call`** whenever there is a `$this` that is an instance of the class being
named — so `self::m()`, `Foo::m()` and `call_user_func(['Foo', 'm'])` all reach
`__call` when made from inside a `Foo` method, and `__callStatic` only from
outside.

The decision therefore needs the **bound object of the innermost call**, not the
calling scope (`Unit.callingThis`, read off the stack the backtrace keeps).
`forwarderFor` makes the choice once for the three paths that need it:
`ZEND_INIT_STATIC_METHOD_CALL`, `staticTarget`, and the callable-validity check.

Getting this wrong is quiet: the call simply reports "Call to undefined method".
Fixing it moved 21 tests, having been logged as a two-test item.

## `static::` for a builtin

`self::` is the executing scope; `static::` is the class the **call site**
named, which an inherited static method reached through a subclass makes
different. A builtin has no frame, so both travel on the Ctx: `Ctx.Scope` and
`Ctx.CalledScope`, set from `frame.class` and `frame.called` on every frame.

Everything that resolves a class *keyword* outside the opcode handlers needs the
second one:

- `is_callable("static::ok")`, `["static", "ok"]` and the rest of `callableClass`
- `defined("static::K")` and `constant("static::K")` — these take a
  `"Class::CONST"` string and, before 2026-08-24, resolved none of the three
  keywords at all

Getting it wrong is quiet: `static` silently behaves as `self`, which is right
whenever the two coincide — so a test only catches it with an *inherited* method
reached through a subclass.

## `as` and `insteadof`

`Class.traits` carries only the trait *names*. The clauses of a `use` statement
live in `ce->trait_aliases` and `ce->trait_precedences`, harvested since
2026-08-24 as `Class.trait_aliases` / `trait_precedences`. Both lists survive
linking, which is what makes them harvestable — a class using a known trait is
already linked when the harvest runs.

- An alias adds a **name**, not a method: `use T { a as b; }` leaves `a` working
  and gives the same body a second name. A clause with modifiers and no new name
  changes only the visibility.
- `A::m insteadof B` removes **B's** `m` from consideration for that class; it
  does not pick a winner so much as eliminate losers.

### The clauses are validated before anything is bound

`zend_traits_init_trait_structures`, so the refusal comes before any output the
class's methods would produce:

| Clause | Refusal |
|---|---|
| names a non-trait | `Class X is not a trait, Only traits may be used in 'as' and 'insteadof' statements` |
| names a trait the class does not use | `Required Trait T2 wasn't added to C` |
| `A::nonExistent as x` | `An alias was defined for A::nonExistent but this method does not exist` |
| `nonExistent as x` | `An alias (x) was defined for method nonExistent(), but this method does not exist` |
| modifiers on a missing method | `The modifiers of the trait method m() are changed, but this method does not exist. Error` — the trailing `Error` is part of the engine's string |
| an unqualified alias two traits provide | names both traits and both qualified forms |

## A private property is a slot of its own

The engine keys **every** private property `\0Owner\0name` and every protected
one `\0*\0name`; only public is plain. A private property therefore never
collides with anything, and two declarations of one name are two properties:

```php
class P { private $x = "p"; }
class C extends P { private $x = "c"; }   // \0P\0x and \0C\0x, both live
class D extends P { protected $x = "d"; } // \0P\0x and x, both live
```

php2go keeps its table on the plain name and gives a private property its own
mangled key **only when another class in the chain declares the same name** --
whatever that one's visibility is. An object with no shadowing has exactly the
table php2go always built, which is what keeps every path that reads by name
working.

| Question | Answer |
|---|---|
| Which slot does `$this->x` mean? | The asking scope's own private one if it has one (`ownPrivateProperty`), else the plain name |
| From a class that declares neither? | Neither -- `Warning: Undefined property`, not the ancestor's value |
| What does `get_object_vars` show? | The first *visible* slot per name, under the plain name; from inside `P` that is `P`'s, from inside `C` it is `C`'s, from outside neither |
| A dynamic property whose name happens to be mangled? | Handed back as it stands. The difference is whether the object has a `Declared` entry for the key |
| Can a *dynamic* property shadow a private one? | Yes, and then there are two. The write happens from a scope that cannot see the private one, so it grows its own entry; inside the declaring class the name still means the private one |
| Is a dynamic property visible from outside? | Always. It is public whatever the class declares under that name -- asking `propVisible`, which looks at the declaration, hides it |
| Does a trait's private property collide with a parent's? | No. `zend_do_traits_property_binding` deletes a colliding entry that is another class's private one and gives the trait its own slot |

Two traps in the harvest, both load-bearing:

- A class that **redeclares** a private property lists only its own in
  `properties_info`. A class that redeclares nothing lists **all** of its
  ancestors' private properties, unmangled, several under the same name. Key by
  the *declaring* class, never by the ancestor whose table it turned up in.
- A `use T` anywhere in a chain delays that chain's binding past the harvest, so
  a subclass's class table can be empty while its objects have slots. When the
  class table says nothing, the object's own `Declared` record is the only
  declaration there is.

Three enumerations implement this, and they are easy to fix one at a time and
leave the others wrong: `visibleProps`, `lazyVisibleProps` (what `foreach` uses
when the class has hooks) and `hookedVisibleProps` (what `get_object_vars`
uses). Each has to resolve the declared property through `propertySlot` and skip
a declared property's own key in its tail loop.

Moving a declared property onto its own key has to preserve position --
`php.Array.Rekey` renames in place -- because the table's order is what
`foreach`, `var_dump` and `print_r` all expose.

## A private method of the calling scope wins whatever resolved

`zend_std_get_method`'s second lookup is not conditional on the *resolved*
method being private:

```php
class A { function __construct() { $this->foo(); } private function foo() {} }
class B extends A { public function foo() {} }
new B;    // A::foo
```

B's override carries `ZEND_ACC_CHANGED` -- set precisely because it overrides a
parent's private method -- and that sends the engine to
`zend_get_parent_private_method`, which asks only the **scope**. So the question
is "does the calling scope declare a private method of this name?", and the
visibility of whatever the object's class resolved to does not enter into it.

Ordinary polymorphism is untouched: a scope with no private method of that name
finds nothing and the override answers.

## The private method of the calling scope wins in every spelling

A private method is per-class, so a call made *inside* `A` reaches `A::m` even
on a `B` instance whose `B::m` overrides it. That is not a property of the
`->` syntax: it holds for every callable shape, because they all resolve
through the same second lookup.

```php
$this->m();                     // ZEND_INIT_METHOD_CALL
$this->$name();                 // same, dynamic name
call_user_func([$this, $name]); // Unit.pendingCallFor
array_map([$this, $name], $xs); // same
```

The lookup is: resolve on the instance's class as usual, then -- if the calling
scope is an ancestor of that class and declares the name **privately** -- take
the scope's declaration instead. Only `private` gets this. A `protected` method
of the calling scope does not beat a subclass override; that is an ordinary
virtual call.

Fixing one spelling and not the others is the failure mode worth naming:
`bug46246` calls the same method three ways in a row precisely so that a partial
fix shows up as two lines agreeing and the third not.

## Trait alias table identity

`use T { m as n; }` installs one method body under two names. The generated
method table must therefore key each entry by the name the class knows, not by
the lowered function body's `fn.Name`; keying both entries by the body name
collapses the alias into a duplicate map key. The original and alias should
continue to share the same lowered body.

Whether the alias belongs in the instance or static table comes from the
trait's own method table. A heuristic over the alias can add it to both tables
and reproduce the duplicate from the other direction. When generated Go reports
a duplicate key, inspect the rendered table: the error names the line, not the
semantic cause.
