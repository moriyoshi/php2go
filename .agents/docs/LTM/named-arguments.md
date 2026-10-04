# Named Arguments

## Summary

`f(name: value)` is resolved against the **callee's** parameter list, and the
callee is not known until `DO_FCALL`. Everything hard about named arguments
follows from that: the resolution has to happen late, it needs a parameter list
for *every* shape a callee can take, and a name that resolves to a position past
the ones the caller filled leaves a hole that somebody has to fill.

php2go does the placement in `applyNamedArguments` (`runtime/interp/call.go`) and
gets the list from `calleeParameters` beside it.

## Key facts

| Fact | Why it matters |
|---|---|
| The name belongs to the callee, not the call site | `call_user_func("f", zzz: 0)` says `Unknown named parameter $zzz` about **f** |
| A variadic callee accepts any name and keeps it as a string key | Those are `extraNamed`, read when the frame is built |
| A hole below the required count is `Argument #N ($x) not passed` | Not the same diagnostic as passing too few arguments |
| A hole at or above the required count is the *callee's* problem | A user function has a `RECV_INIT`; an engine function has only text |
| An engine function's default is source text on the `arg_info` | `' '`, `null`, `STR_PAD_RIGHT`, `A \| B \| C` — parsed, not evaluated |
| An *optional* engine parameter with no default text cannot be filled | `array_keys(): Argument #2 ($filter_value) must be passed explicitly, because the default value is not known` |
| A class that declares no constructor has an **empty** parameter list | `new stdClass(x: 1)` is `Unknown named parameter $x`, not a gap |

## Which callees need a parameter list

Every one of these was, at some point, reported as
`unsupported: named arguments to X` because nothing could answer for it:

| Callee | Where the list comes from |
|---|---|
| A function or method with an op_array | the op_array's `args` |
| A closure | `closure.op` |
| `$closure->__invoke(...)` | the closure's own signature — the engine answers with a trampoline carrying it |
| `$closure->call($newThis, ...)` | `$newThis` prepended to the closure's, so a name lands one position along |
| `ReflectionFunction::invoke` | the reflected function's |
| `ReflectionMethod::invoke` | `$object` prepended to the method's |
| `ReflectionClass::newInstance` | the constructor's |
| An engine function | the harvested `Builtin` |
| An engine method | the harvested `EngineMethod.args` |
| A class with no constructor at all | empty, and *known* to be empty |
| `parent::$p::set()` with no hook written | one parameter, `$value` |

The `*Args` forms (`invokeArgs`, `newInstanceArgs`, `call_user_func_array`) take
the same names as **string keys in the argument array**, which is the same
resolution by another spelling (`namedFromArray` + `resolveArgumentNames`).

## Forwarding

`call_user_func` forwards the names it has no parameter for to its callee.
Probed, both directions:

- `call_user_func("f", b: 0)` runs `f` with `$b = 0`.
- `call_user_func(callback: "f")` names `call_user_func`'s *own* parameter.
- `call_user_func("f", zzz: 0)` is `Error: Unknown named parameter $zzz`.

`forward_static_call` is **not** the same shape: it answers
`ArgumentCountError: forward_static_call() does not accept unknown named
parameters`. Do not assume the siblings behave alike; ask.

That refusal is the *general* rule for an engine variadic, not a quirk of
`forward_static_call`. `Z_PARAM_VARIADIC` sets `ZPP_ERROR_UNEXPECTED_EXTRA_NAMED`
whenever the call carries extra named parameters; only
`Z_PARAM_VARIADIC_WITH_NAMED` keeps them, and just nine declarations in the tree
use it -- `call_user_func`, `Closure::__invoke`, `Closure::call`,
`Fiber::start`, and the four Reflection invokers. Every one is a forwarder.
php2go holds the list as `namedVariadicCallees` in `call.go`.

**The count is checked first.** `ZEND_PARSE_PARAMETERS_START` compares the
number of arguments that landed in a slot against the minimum *before* reaching
the variadic macro, so `call_user_func_array("array_multisort", ["" => 1])` is
`array_multisort() expects at least 1 argument, 0 given` and says nothing about
the name.

The leftovers travel in `Unit.forwardedNamed` rather than in `pending`, because
`pending.extraNamed` is consumed when a *frame* is built and no builtin builds
one. Every builtin call moves them out at the same boundary, so they can never
be picked up by a later call that never had any.

## Pitfalls

- **A hole is not null.** Leaving `php.Undef()` in the slot for an engine
  function silently passes null, which shows up as a deprecation about the
  *type* of a parameter the program never mentioned.
- **`visibility()` on a modifier word.** Trait alias modifiers are the same kind
  of bitmask; mapping a non-zero value through a visibility helper turns
  `as final` into `public`. Check for the PPP bits specifically.
- **Parsing a default is not evaluating it.** The stub forms are small and
  closed; anything outside them should be an `UnsupportedError`, never a guess.

## op2 is the position, except when it is the name

`ZEND_SEND_VAR_EX` and its siblings carry the argument's *position* in op2 --
unless the argument is named, when op2 holds the parameter's name instead.
Reading it as a position for a named argument asks about whichever parameter
sits at that constant's index, and php2go did exactly that: when the answer
happened to be "by reference" it appended the value **positionally**, so a later
name found the slot already filled.

```
function t(&$a = null, $b = null, &$c = null) { $a++; $c++; }
t(a: $x, b: $y);   // worked
t(b: $y, a: $x);   // Error: Named parameter $a overwrites previous argument
```

Only visible when the callee is declared *after* the call: with the signature
known the compiler emits `SEND_REF`/`SEND_VAR` and nothing is decided at run
time.

Two things follow, and one without the other is worse than neither:

- resolve a const op2 **by name** before asking whether the parameter is by
  reference (`Interp.sendArgumentByRef`);
- keep named arguments off the positional by-reference branch, or they are
  appended as well as named.

A name **no parameter claims** goes to the variadic, and a by-reference variadic
wants its slot too -- so the fallback is "is the last parameter a by-reference
variadic?", and `ZEND_RECV_VARIADIC` stores the slot rather than a copy for the
named entries as well as the positional ones.

There is a **third** place the same mistake fits. An array element passed by
name goes through a pair of opcodes:

```
ZEND_CHECK_FUNC_ARG      UNUSED  CONST("c")   -- names the parameter
ZEND_FETCH_DIM_FUNC_ARG  CV:0    CONST(2)     -- read or write, decided by the above
ZEND_SEND_FUNC_ARG       VAR:2   CONST("c")
```

The fetch cannot tell which argument it is filling -- its `extended_value` is 1
for every named argument -- because the engine keeps the answer on the *call*:
`CHECK_FUNC_ARG` sets `ZEND_CALL_SEND_ARG_BY_REF` and the fetch reads that flag.
php2go models it with `frame.funcArg`, which therefore needs the same
name-to-position resolution.

## forward_static_call_array splits its array

A string key names a parameter, and a positional element after one is
`Cannot use positional argument after named argument`. The **singular** form is
different again: it is not on the `Z_PARAM_VARIADIC_WITH_NAMED` list, so it
answers `forward_static_call() does not accept unknown named parameters`. The
array form has no such refusal and passes the names on.

## Compiled calls can bind names when the target is known

The emitted call protocol is positional, so the compiler resolves names at the
call site when it can prove the callee's parameter layout. This does not reorder
evaluation: lowering has already evaluated every argument expression into SSA
values before the call node is assembled.

Known targets include literal functions, static methods, constructors and a
method name for which every possible declaration agrees on parameter names and
positions. Inherited constructors require a flattened lookup over the parent
chain. A class with `__call` prevents the agreement shortcut because the
trampoline may answer for a method no declaration names.

A skipped parameter is represented by undef, not null, so `recv` applies its
default. Requiredness comes from the op_array's required-parameter count and is
checked by position: a syntactic default before a later required parameter does
not necessarily make the earlier position optional.
