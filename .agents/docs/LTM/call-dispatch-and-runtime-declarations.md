# Call Dispatch and Runtime Declarations

## Summary

Zend separates preparing a call from performing it. The target can be a named
function, method, static method, constructor, callable value, closure, magic
forwarder or first-class callable, and several of those are not known until run
time. The implementation needs one dispatch model carrying target, scope,
called scope, receiver, arguments and source position until the eventual call.

Declarations can also be executable instructions. A function or class nested
under control flow exists in the harvested unit but is unavailable until its
`DECLARE_*` opcode runs.

## Call state

A prepared call records:

- the resolved body or the information needed to resolve it later;
- the declaring scope and late-static called scope;
- the bound `$this`, if any;
- positional, named and by-reference argument state;
- the call-site file and line for diagnostics and traces;
- constructor bookkeeping and magic-forwarder identity.

The execution opcode consumes that state. Treating a first-class callable node
as a call loses the distinction: `f(...)` constructs a Closure-equivalent
object and invokes nothing until that object is called.

`runtime/php` exposes a `CallValue` hook because it owns neither a program's
function table nor its classes. The interpreter and generated unit supply the
resolver; library functions such as `array_map` and `call_user_func` then share
the same callable semantics as direct dynamic calls.

## Scopes are separate values

Four identities are easy to conflate:

| Identity | Used for |
|---|---|
| declaring class | `self`, private members, frame name |
| called class | `static`, inherited static calls |
| calling scope | visibility and callable validation |
| bound receiver | `$this`, `__call` preference and instance calls |

Static syntax can still retain an instance. When the caller has a compatible
`$this`, PHP may choose `__call` for a static-looking call and use
`__callStatic` only when no such receiver applies. The decision therefore
cannot be reconstructed from spelling alone.

Method lookup also gives a calling scope's own private method precedence over a
subclass override for every spelling, including dynamic calls and callback
arrays.

## Callable values

Callable parsing validates shape, class availability, visibility, staticness
and magic fallback. Compound method strings such as `parent::m` resolve their
scope against the class/object named by the callable, while bare `parent` or
`static` keywords resolve against the caller. Deprecated callable spellings are
accepted with the engine's warning rather than refused.

`Class::method` first-class callables are static-call shapes, not generic
two-element arrays. A method callable retains its object when it has one.
Invalid callbacks carry a reason specific to the public API; a plain `$f()`
dynamic call can use a different error form.

## Autoload and availability

The harvest knows bodies the program has not declared yet. Therefore:

- finding a harvested class is not the same as it being declared;
- class existence, link availability and body lookup are separate predicates;
- dependency resolution at `DECLARE_CLASS` may run autoloaders;
- autoload callbacks run in registration order with a recursion guard and stop
  as soon as the requested class becomes available;
- an autoloader is a call and temporarily changes the current source position,
  which must be restored before reporting a missing dependency.

In a standalone compiled artifact there is no compiler to load new PHP source.
An autoloader can still succeed through declarations already harvested or
`class_alias`; it must not be omitted merely because filesystem compilation is
unavailable.

## Conditional declarations

An unconditional top-level function is present in the unit's function table. A
conditional `function f() {}` lowers to `DECLARE_FUNCTION`; its body is stored
with nested op arrays and a wrapper under the public name refuses until the
instruction executes.

`function_exists` asks a broader question than “can this generated unit call the
name”: it checks runtime-declared functions, library functions and the engine's
harvested function list. The same declared-versus-known distinction applies to
classes and is critical for autoloading.

## Names and arguments

PHP identifiers in the runtime tables are case-insensitive where the engine
says so, but their display spelling is not. Namespaced function/constant
fallback carries both candidates in the IR; reconstructing it from a backslash
in the string is unsound.

Named arguments bind to parameter positions at the call site once the callee is
known and all argument expressions have already been evaluated. Dynamic or
library callees need their harvested parameter list at run time. Argument
unpacking, variadics and by-reference requirements preserve names beyond a
simple positional slice.

## Relevant files

- `runtime/interp/call.go`, `callback.go`, `autoload.go`, `closure.go` —
  interpreted dispatch and declarations.
- `runtime/php/callable.go`, `frame.go`, `ctx.go` — shared call hook and frame
  state.
- `internal/gogen/gogen.go` — generated wrappers, call tables and first-class
  callable construction.
- `internal/zend/zend.go`, `proto/php2go/ir/v1/ir.proto` — harvested names,
  signatures and declaration op arrays.

## Test coverage

Relevant differential fixtures include `callbacks.php`, `static_callables.php`,
`first_class_callables.php`, `conditional_functions.php`, `autoloading.php`,
`named_arguments.php`, `call_user_func_sends.php` and
`method_visibility.php`.

## Pitfalls

- Do not use the harvest's presence as runtime declaration state.
- Do not use calling scope as a substitute for a bound receiver.
- Do not validate every callable at the same time; construction, registration
  and invocation APIs intentionally differ.
- A call through a builtin still needs an observable frame when PHP attributes
  errors or backtraces to that function.
