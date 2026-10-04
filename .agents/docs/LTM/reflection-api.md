# The Reflection API

## Summary

Reflection is not a view of php2go's own data structures; it is a view of what
the engine's `reflection_object` would hold. Nearly every gap found so far came
from answering with the obvious php2go value instead of the one
`ext/reflection` computes -- and the two differ most where a `Closure` is
involved, because the engine manufactures declarations that were never written.

The reflectors all hang off one `reflector` struct (`runtime/interp/
reflection.go`); which field is set says what kind it is. `reflectdump.go`
implements the `__toString` forms, which are `_function_string`,
`_class_string`, `_property_string` and friends.

## Key facts

| Fact | Why it matters |
|---|---|
| `Closure::__invoke` is a **trampoline**, not a method | `ReflectionMethod($closure, "__invoke")` answers `__invoke` / `Closure`, with the *wrapped* function's parameters, attributes and deprecation |
| The trampoline is an **internal** declaration | `(string)` prints `<internal>`, no `@@` line, and `= <default>` rather than the value |
| A closure attribute targets a **function** | Even when the closure was declared inside a class and carries its scope |
| A first-class callable is a **fake** closure | Both `ZEND_ACC_CLOSURE` and `ZEND_ACC_FAKE_CLOSURE`; it targets a *method*. php2go tracks this as `closure.fake` |
| A closure's attributes evaluate in its scope | `#[A(self::FOO)]` answers the declaring class, and the rebound class after `bindTo` |
| `$name` and `$class` are unwritable on every reflector | `_reflection_write_property` is a two-name check on any class that declares the property, not a `readonly` flag -- so it applies from every scope |
| `ReflectionAttribute` declares `public readonly string $name` | `var_dump` of one shows it; a reflector that only answers `getName()` dumps `uninitialized(string)` |
| A reflector's `name` may not be the declaration's | A trait alias renames: `use T { test as __construct; }` reflects and prints as `__construct` |
| A **dynamic** property is reflectable through its object | `new ReflectionProperty($obj, "dyn")` works; through the class name it is `Property C::$dyn does not exist`. `isDefault()` is false, `(string)` prints `<dynamic>` |
| `isInitialized()` needs the object even when the reflector had one | The engine keeps no reference: `Argument #1 ($object) must be provided for instance properties` |
| A Closure dumps as `Closure [` | `(string)` heads with `Closure [` instead of `Function [` / `Method [`, however the closure was made -- the rest of the line still says `function` or `public method` |
| Reflection describes **static** properties | Both `new ReflectionProperty(C::class, "s")` and `ReflectionClass::getProperty` see them, though the instance-property lookup hides them |
| `setValue($v)` with one argument is deprecated | `setValue(null, $v)` is not. The deprecation is about the *argument count*, not about statics |
| Reflection keeps the **real** class name | `getName()`, `getShortName()` and `$name` all carry an anonymous class's `\0file:line$hash`, so the reflector compares equal to `get_class()` |

## The engine's own target test

`ReflectionFunctionAbstract::getAttributes` does not ask whether the function
has a scope:

```c
if (fptr->common.scope
    && (fptr->common.fn_flags & (ZEND_ACC_CLOSURE|ZEND_ACC_FAKE_CLOSURE)) != ZEND_ACC_CLOSURE) {
    target = ZEND_ATTRIBUTE_TARGET_METHOD;
} else {
    target = ZEND_ATTRIBUTE_TARGET_FUNCTION;
}
```

So a closure *written as one* is a function target even inside a class, and a
fake closure is a method target even though it is a `Closure` instance. Getting
this wrong is invisible until an attribute declares
`Attribute::TARGET_FUNCTION` and the reflector refuses it.

## `ReflectionClass::newInstance` and the fake scope

`newInstance` / `newInstanceArgs` look the constructor up with `EG(fake_scope)`
set to the **reflected class**, and which diagnostic comes out depends on what
that lookup finds:

| constructor | result |
|---|---|
| public | runs |
| non-public, **declared** by the class | `ReflectionException: Access to non-public constructor of class C` |
| non-public, **inherited** | `Error: Call to private X::__construct() from scope C` |

The inherited case never reaches the reflector's own check: a private
constructor is invisible even from the subclass's own scope, so the lookup
fails first and the ordinary Error is raised -- naming the *reflected* class as
the scope, which is the only sign the fake scope was in play.

Everywhere else, a constructor refusal names the class that **declared** the
constructor, not the one being instantiated.

## `Closure::__invoke` outside reflection

The trampoline is not only a reflection object. `[$closure, "__invoke"]` as a
*callable* goes through it too, and a diagnostic raised against such a call
names `Closure::__invoke()` where a plain `call_user_func($closure, ...)` names
`{closure:FILE:LINE}()`. A backtrace still shows the closure's own compiled
name, with `Closure->__invoke()` as the frame above it -- so the trace name and
the diagnostic name are genuinely two different things
(`pendingCall.invokedAs`).

## Deprecation labels

`#[\Deprecated]` on a called function produces one of two shapes, and which one
is chosen by whether the callee has a scope, not by what kind of value the
program held:

- no scope: `Function {closure:FILE:LINE}() is deprecated, why`
- a scope: `Method D::m() is deprecated, m`

For a closure that wraps a method the second is used, with the **plain** method
name: a trace name (`D->m`) put into that slot produces `Method D::D->m()`.
The live call site is `DO_FCALL` in `interp.go`; `call.go` has a second one for
callbacks and engine-invoked handlers, and fixing only that one changes
nothing.

## Pitfalls

- **`Raise` is not a throw.** `Raise` builds a `*phpError` that becomes a
  throwable only when something calls `materialize`. A builtin *method* return
  passes through it; a property-write path hands the error straight to
  `f.raise`, which does not. Use `in.throwError(ctx, unit, ...)` there. The
  symptom is `--- INTERPRETER (error) ---` with the right message and no PHP
  `Fatal error:` banner.
- **A right answer for the wrong reason still fails the differential.**
  Reflecting the *wrapped* method for `ReflectionMethod($closure, "__invoke")`
  makes `gh17866` pass; the engine says `Closure::__invoke`. Probe the whole
  reflector, not just the field the failing test prints.
- **`DisplayName` is a diagnostic's rule, not a reflector's.** Cutting a name
  at its NUL is what the engine's printf-built messages do. Reflection, `==`
  and `get_class()` all keep the suffix; using `php.DisplayName` in a reflector
  silently breaks anonymous classes and nothing else.
- **Giving a reflector a scope changes more than name resolution.** It also
  decides the attribute target and the `__toString` header. See
  [visibility-and-composition](./visibility-and-composition.md) for the
  scope rules themselves.

## The property list is not the property table

`ReflectionClass` answers from `ce->properties_info`, which differs from the
object's slot layout in two ways:

- An ancestor's **private** property is not in it. `hasProperty`,
  `getProperty`, `getProperties` and `ReflectionProperty::__construct` all
  apply the same test, `(pptr->flags & ZEND_ACC_PRIVATE) && pptr->ce != ce`.
  The instance lookup must *not* apply it -- `$this->b` inside the declaring
  class still reaches the slot -- so this filter belongs only at the reflection
  call sites (`reflectableProperties`, not `declaredProperty`).
- The **order** is each level's own declarations, class first and then
  ancestors nearest first. Inheritance appends the parent's entries after the
  class's own were compiled. The property table is the other way round, by slot
  offset, base first. A trait's contribution counts as the using class's own and
  follows the class body, since the binding is a link-time step.

A property that redeclares an inherited one keeps the inherited *offset*, so
offset order cannot recover its source position; see `../TODO.md`.

Within a level the order is **declaration** order, which is a second ordering
the harvest has to carry: the property table travels sorted by slot offset --
what `var_dump` and `foreach` expose -- and a **static** property has no slot
among the instance ones, so deriving one order from the other puts it at the
end instead of where it was written. `Property.order` is the index in
`properties_info`; `reflectableProperties` sorts each level by it.

The two lazy writers name the class that **declared** the property,
`prop->ce`, not the class the reflector was built from -- except for the
*dynamic* refusal, which has no declaration to ask and names the reflector's
own class.
