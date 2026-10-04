# Lazy Objects

## Summary

PHP 8.4's lazy objects are an object with a class and a handle but no state,
whose state appears the first time anything looks at it. Two strategies: a
**ghost** is filled in place by its initializer, a **proxy** hands off to a real
instance the initializer returns. php2go models them in `runtime/interp/
lazy.go` with `php.Lazy` on the object.

The load-bearing idea is that laziness is **per property**, not per object.
`zend_lazy_objects.c` counts `lazy_properties_count`; php2go carries the same
thing inverted, as `Lazy.Skipped`. Everything that behaves oddly here is
something that took a property out of the deferred set.

## Key facts

| Fact | Why it matters |
|---|---|
| An object with no deferred property is **not lazy** | Its initializer never runs. `allSkipped` ends the laziness the moment the last property is given a value |
| `skipLazyInitialization` marks a property non-lazy | It writes the declared default; `setRawValueWithoutLazyInitialization` writes a given value and type-checks it |
| A ghost's initializer starts from the declared defaults | `restoreDefaults` runs first, and it must skip the non-deferred properties or it wipes them |
| A ghost that throws is rolled back | To the state *before* the defaults were restored -- not to empty, since a raw write may already have filled slots |
| `resetAsLazyGhost` keeps a final class's readonly value | See below; the kept property is also non-deferred |
| A raw write is a write | `setRawValueWithoutLazyInitialization` on an initialized readonly property is `Error: Cannot modify readonly property`. `skipLazyInitialization` writes nothing and is never refused |
| Neither writer asks whether the object is *lazy* | `reflection_property_check_lazy_compatible` refuses a **dynamic**, **static** or **virtual** property and nothing else. On an ordinary object the raw write is an ordinary write and skipping does nothing |
| Skipping a property that is already not lazy is a no-op | It does not write the declared default over what the program has since put there. A raw write does overwrite |
| A virtual hooked property defers nothing | It has no slot, so reaching it produces no state |
| `ReflectionProperty::isLazy` is false for three kinds | A **static** belongs to the class, a **virtual** one has no storage, a **dynamic** one was written after the object was made |
| All three lazy `ReflectionProperty` methods follow an initialized proxy first | `while (zend_object_is_lazy_proxy(object) && zend_lazy_object_initialized(object)) object = get_instance(object);` -- a *loop*, since an instance may itself be a proxy. `lazyTarget` |
| A reset happens *between* lazy states | `resetAsLazyProxy` on an initialized proxy releases the instance while the object is not lazy, so the instance's destructor sees an ordinary `object(A)#n` rather than a `lazy proxy` one |
| A reset proxy's own table holds untyped empty slots | Which `var_dump` prints nothing at all for: `if (!Z_ISUNDEF_P(val) || prop_info)`, and `prop_info` is only set for a *typed* property. See [object-lifetime](./object-lifetime.md) for the other half of what a reset makes visible |

## The reset rule

`resetAsLazyGhost` / `resetAsLazyProxy` unset the declared properties so the
initializer may write them again. One exception, from `zend_lazy_objects.c`:

```c
if ((prop_info->flags & ZEND_ACC_READONLY) && !(Z_PROP_FLAG_P(p) & IS_PROP_REINITABLE)
        && ((obj->ce->ce_flags & ZEND_ACC_FINAL) || (prop_info->flags & ZEND_ACC_FINAL))) {
    continue;
}
```

A readonly property that already holds a value survives when the object's class
is final -- nothing can reach it to write it legitimately, so the value stands
and the reconstructing `__construct` is refused. A non-final class clears it and
`__construct` may set it once more. This is the entire difference between the
two halves of `reset_as_lazy_readonly`.

**The kept property must also be marked non-deferred.** Keeping the value alone
is not enough: the ghost's `restoreDefaults` pass wipes it on the way into the
initializer unless `Lazy.Skipped` says it is not lazy. Setting that mark also
gets the tail case right for free -- reset a final class whose only property is
a kept readonly one and nothing is deferred, so the object is not lazy and the
initializer never runs.

The `final` half of the condition is on the *property* as well as the class,
which php2go does not model: `Property.final` is not harvested. No test is known
to depend on it.

## Pitfalls

- **"Cleared nothing" is not the same as "defers nothing."** Counting what the
  reset cleared gets the common case right and the general one wrong. Mark the
  kept properties `Skipped` and let `allSkipped` answer.
- **A reflector's refusals have an order.** dynamic, then static, then "object
  is not lazy". Getting it backwards turns a specific diagnostic into a generic
  one, and the tests check the message.
- See [reflection-api](./reflection-api.md) for what `ReflectionProperty`
  itself answers, which is where most of these calls enter.

## What the initializer may hand back

| Kind | Returns | Result |
|---|---|---|
| ghost | null, or nothing | the object is filled in and stops being lazy |
| ghost | anything else | the initialization is **reverted** and `TypeError: Lazy object initializer must return NULL or no value` |
| proxy | a non-object | `TypeError: Lazy proxy factory must return an instance of a class compatible with C, null returned` |
| proxy | an incompatible object | `TypeError: The real instance class X is not compatible with the proxy class C. ...` |
| proxy | the proxy itself, or any lazy object | `Error: Lazy proxy factory must return a non-lazy object` -- checked *after* compatibility |

Compatibility is **counted**, not searched for. The proxy class must be an
instance of the real one, the two must have the same number of entries in
`properties_info`, and `__destruct` / `__clone` must resolve to the same
declaration. Counting is what makes an *override* free:

```php
class B { public int $b; }
class D extends B { public int $b; }   // same entry -- D may proxy a B
class F extends B { public int $extra; } // one more -- F may not
```

`default_properties_count` is the wrong number: the engine's own comment says
it is off by the number of overrides.

## Which slots a reset walks

`resetAsLazyGhost` / `resetAsLazyProxy` defer the *reflected* class's
properties, not the object's:

- Reset an instance of `class B extends A` through `ReflectionClass(A::class)`
  and B's own properties keep their values and are not deferred -- their slots
  are past the end of what the engine walks.
- A **dynamic** property has a slot in neither class and is cleared with the
  rest.
