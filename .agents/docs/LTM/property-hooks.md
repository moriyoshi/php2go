# Property Hooks

## Summary

PHP 8.4's `public $p { get { ... } set { ... } }`. The hook bodies are op_arrays
that hang off the property rather than living in the class's function table, and
every property-access opcode has to route through them. The one rule that is
easy to get wrong — and that stack-overflows when you do — is how the engine
decides an access is *inside* the hook.


## Asymmetric visibility is checked before the hooks

`public private(set) string $p { set => $value; }` written from outside is
`Cannot modify private(set) property C::$p` -- the set hook never runs. A set
hook is the *implementation* of the write, not a way around the rule, and a
hooked property with no set hook is checked identically. Three write paths need
the check: plain assign, compound assign, and increment.

## What reflection says about one

| property | isVirtual | hasHooks | hasDefaultValue | getDefaultValue |
|---|---|---|---|---|
| `$v { get {...} }` | 1 | 1 | **0** | NULL |
| `$p = 1` | 0 | 0 | 1 | 1 |
| `$q` untyped, no initializer | 0 | 0 | **1** | NULL |
| `int $t` typed, no initializer | 0 | 0 | 0 | NULL |
| dynamic | 0 | 0 | 0 | NULL |

A hooked property has no default even when the source wrote one; an untyped
property has one whether or not it was written. `getHook(PropertyHookType::Get)`
answers a `ReflectionMethod` named `$v::get` -- the name the engine compiled the
hook under, not the property's.


## Key facts

| Fact | Where |
|---|---|
| A hook is an op_array named `$p::get`, stored in `zend_property_info.hooks[]`, **not** in `ce->function_table` | `zend_compile.c` ~8555 |
| `ZEND_ACC_VIRTUAL` marks a property with no backing storage | `zend_compile.h` |
| A property is backed when a hook mentions `$this->p`; the engine decides and flags it | — |
| "Am I inside this hook?" compares `prop_info->prototype`, not the property itself | `zend_is_in_hook`, `zend_object_handlers.c` |
| …and the object too: same prototype, different object → the hook *is* called | `zend_should_call_hook` |
| A get-only property is read-only only when **virtual**; a backed one writes its storage | `backed_implicit_set.phpt` |
| `unset($o->p)` on a hooked property is always an Error | — |
| `parent::$p::get()` is its own opcode, with a trampoline when the parent has no such hook | `ZEND_INIT_PARENT_PROPERTY_HOOK_CALL` |
| A write *through* a hooked property needs a reference or an object back from the get hook, or it is refused | `zend_std_read_property`, `BP_VAR_W` |
| `&get` is `ZEND_ACC_RETURN_REFERENCE` on the hook's op_array -- the same flag `function &f()` sets | harvested as `OpArray.returns_ref` |

## Details

### The recursion rule

Inside a hook, `$this->p` reaches the backing store instead of calling the hook
again — but only *directly* inside it. A hook that calls a method which touches
the property does re-enter, and PHP lets that recurse until the stack gives out.

Comparing the property by identity is not enough:

```php
class A { public $p = 1 { get { return $this->p * 2; } } }
class B extends A { public $p { get { return parent::$p::get() + 100; } } }
var_dump((new B)->p);   // int(100)
```

Inside A's get hook the object's class is B, so a lookup by object class finds
*B's* property — a different `zend_property_info`. The engine compares
prototypes, and B's redeclaration prototypes to A's, so it matches and the
access goes to the backing store. B never initialised it, so `null * 2 == 0`,
plus 100.

php2go has no prototype pointer, so `hookIdentity` is `(property name, root
declaring class)` — the topmost ancestor declaring the property, the same "root"
idea `methodRoot` needs for protected access. The identity travels on the frame
(`frame.hook`), which is what makes the check exact: it is about the currently
executing function, not about a stack of active hooks.

### Backed versus virtual

- **Backed, no set hook** — a write goes straight to the storage (the implicit
  set). This is *not* read-only.
- **Virtual, no set hook** — `Error: Property C::$p is read-only`.
- **Virtual** — no slot on the object at all: absent from `var_dump`,
  `get_object_vars` and `foreach`. `instantiate` must skip it.

### Which opcodes need wiring

`FETCH_OBJ_R`, `FETCH_OBJ_IS`, `ASSIGN_OBJ`, `ASSIGN_OBJ_OP`,
`PRE_INC_OBJ`/`PRE_DEC_OBJ`/`POST_INC_OBJ`/`POST_DEC_OBJ`,
`ISSET_ISEMPTY_PROP_OBJ`, `UNSET_OBJ`, and
`ZEND_INIT_PARENT_PROPERTY_HOOK_CALL`. The compound and increment forms are
read-through-get, combine, write-through-set — there is no slot to update.

`isset()` runs the get hook and asks whether the result is null; it does not
consult the storage.

`FETCH_OBJ_W`/`_RW`/`_UNSET` are wired as of 2026-08-24. In write mode the
engine runs the get hook and then insists on one of two things:

- a **reference** -- which only a `&get` hook returns, and which the write
  reaches straight through;
- an **object** -- which can be mutated without writing the property back, so
  the fetch hands out a copy of the handle;

and anything else is `Error: Indirect modification of C::$p is not allowed`.
Note this catches the *backed, set-only* property too: `public $p = [] { set
{...} }` has no get hook, the backing array comes back by value, and `$o->p[] =
1` is refused.

Still unwired: `FETCH_OBJ_FUNC_ARG` and the hooked-object iterator.

## Files

- `internal/zend/zend.go` — `php2go_prop_hook`, `php2go_prop_is_virtual`,
  `convertProperties`.
- `proto/php2go/ir/v1/ir.proto` — `Property.get_hook`, `set_hook`, `virtual`.
- `runtime/interp/hooks.go` — `hookIdentity`, `hooksFor`, `hookedRead`,
  `hookedWrite`, `hookedCompound`, `parentHookTarget`.
- `runtime/interp/interp.go` — the opcode sites; `frame.hook`, `runHook`.
- `third_party/php-src/Zend/zend_object_handlers.c` — `zend_is_in_hook`,
  `zend_should_call_hook`, `zend_call_get_hook`.

## Test coverage

Five differential cases in `runtime/interp/differential_test.go` cover the
read-only error, a backed property through get/set/compound/increment, the
parent-hook chain, virtual properties with `isset`/`empty`/`unset`/`var_dump`,
and the implicit set.

## Pitfalls

- Comparing the property message by pointer looks right and recurses forever the
  moment a subclass redeclares a hooked property. Compare the root.
- `get_class_methods` does not list hooks, so "the class has no methods" is not
  evidence that hooks are absent.
- The read-only error says "read-only", not "readonly" — the latter is the
  separate modifier's wording.

## The hooked enumerations walk a different order than the table

`get_object_vars`, `foreach` and `json_encode` on a class with hooks are built
by walking the *names*, not the slots. The order is: the parent's declarations
first, then each subclass's new ones, with an **override keeping the position it
overrides**.

That differs from the property table, which is sorted by slot offset, because a
hooked property that overrides a plain one does not keep the slot it overrides:

```php
class T { public $a = "a"; public $v { get => "V"; } public $b = "b" { ... } }
class C extends T { public $a = "a" { get => strtoupper(parent::$a::get()); } }
get_object_vars(new C);        // a, v, b
var_dump(new C);               // a, b -- the table, and only what has a slot
```

`Property.order` alone is not enough: it is the index in `properties_info`,
which is *child-first*, so sorting the flat list by it gets a pure override
right and a subclass that adds names wrong. Walk the chain root-first instead.

A virtual property may not have a default value, so a hooked property written
with one has to read its backing store in the hook.

## Which hook, and whether the property is visible, both depend on the scope

A property walk carries a scope of its own, and it is not always a frame's:
`get_object_vars($this)` inside a parent, on an instance of a subclass, asks as
the **parent**.

| Question | Wrong answer | Right one |
|---|---|---|
| Is it visible? | `propVisible` -- about the declaration the subclass's table holds | `propertyAccess`, which knows a scope means its *own* private declaration |
| Whose hook runs? | `hooksFor`, which reads `f.class` and a walk has no frame | `hooksForScope`, given the scope |

## var_export runs the get hooks

It includes a virtual property, writes every entry under its plain name, and
keys the entries by **slot**: two classes' private properties of one name are
two entries, and an override of a public one is a single entry answered by the
most derived declaration. Duplicate names in the output are fine -- it is text,
not an array.

That makes `var_export` need an interpreter: a get hook is a call. php2go
threads a resolver through the exporter, nil for the plain builtin.

## A hook is a frame

```
#0 file(line): H->$p::set(2)
#1 {main}
```

The op_array's own name is `$p::set`, so the frame is the **declaring** class,
`->`, and that -- a subclass that inherits the hook still shows the parent.
`#[\SensitiveParameter]` on a set hook's parameter hides the argument there
exactly as on a method's.

php2eval cannot ask about either: its engine bails on any *write* through a set
hook and on that attribute inside a hook. Get hooks are fine.

## A hooked property reached as storage

The compiled place path must preserve `Function.ReturnsRef`; a get hook and an
`&get` hook share the same op_array shape except for that flag. Opening a hooked
property for indirect modification first performs its ordinary read, including
hook output and write-only errors, and only then decides whether storage can be
handed out:

- `&get` returns the box named by the hook body;
- an ordinary value is refused with `Indirect modification of C::$p is not
  allowed`;
- an object value is allowed because mutation reaches the same handle without
  writing the property;
- binding *to* the hooked property is always refused, because it would bypass
  the set hook on later writes.

All of these checks step aside while the hook itself is executing. A set hook
may legitimately assign or bind the raw backing slot, and treating that access
as another external hooked write recurses or invents a refusal.

`parent::$p::get()` and `::set()` walk the parent chain until a hook is found.
When no ancestor defines the requested hook, the operation is a raw access to
the parent's backing store. The hook kind comes from an unused op2 payload (0
for get, 1 for set), not from the source spelling.
