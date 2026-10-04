# Honest Gaps versus PHP Errors

## Summary

php2go reports two kinds of failure and they must not be confused. A catchable
PHP error means the *program* did something wrong; an `UnsupportedError` means
the *interpreter* has a gap. Keeping them apart is what makes the pass rate mean
anything.

## Diagnostics that name the wrong thing

A message can be the right *kind* and still be wrong, and the corpus checks the
text. Recurring shapes, all probed:

| Written | Engine says | Easy mistake |
|---|---|---|
| `["stdclass", "b"]()` | `Call to undefined method stdClass::b()` | echoing the spelling from the callable instead of the class entry |
| `$v::class` on a non-object | `TypeError: Cannot use "::class" on string` | sharing the static-call operand resolver, where a string is a class name |
| `new Y` on an inherited private ctor | `Call to private X::__construct() from scope Y` | naming the class being instantiated |
| `$s[] += $s` | `[] operator not supported for strings` | reporting the operator refusal that a *real* offset earns |
| an uncaught fatal with no file | `thrown in Unknown on line N` | printing the empty string the exception's own `(string)` form uses |

Two general rules behind them: name a class from the **class entry**, not from
the text that reached it; and when two refusals could apply, ask the engine
which comes first rather than picking the more specific one.

The first rule extends to functions and methods: a trace and an
`ArgumentCountError` both name the **declaration**, so `call_user_func("exploDe")`
reports `explode()` and `call_user_func("k::M")` reports `K::m()`.

There are also two spellings for "no file": `zend_error` says `Unknown`, an
exception's own `getFile()` says `[no active file]`.


## Key facts

- **Catchable PHP error** — raised through
  `in.throwError(ctx, unit, "Error", …)`. It has to be thrown, because the
  program may catch it and the test usually expects its exact text.
- **`UnsupportedError`** — an interpreter gap, counted separately by `php2phpt`
  so the pass rate stays honest.
- The rule for telling them apart is always "could this build do it?", answered
  from harvested data rather than guessed.

## Details

### The decision table

| Situation | Verdict |
|-----------|---------|
| Function in `Manifest.builtins` but unimplemented | gap → `UnsupportedError` |
| Function absent from the linked build | `Error: Call to undefined function f()` |
| `unit.isBuiltinOnly(class)` — an engine class the runtime does not model | gap |
| Class declared by the program, method missing | `Error: Call to undefined method C::m()` |
| Name resolving to nothing at all, after autoloading | `Error: Class "X" not found` |

### Converting Go errors into PHP errors was worth more than it looks

`can only throw objects`, `__clone method called on non-object`, `too few
arguments`, undefined constants, and undefined classes were all escaping as bare
Go errors from the harness. Tests that *expected* those messages could never
pass. Each is now a catchable PHP error with the engine's exact wording,
including `ArgumentCountError`'s "exactly N" versus "at least N" and the
caller's file and line, which comes off the call stack.

Stack traces themselves: `Ctx` carries the call stack, and an exception
snapshots it at *creation* — which is also when PHP records `file` and `line`,
not at throw. Argument rendering matches the engine exactly, elision included:
`C->m('a-very-long-str...', Array, Object(stdClass), true, NULL, 1.5)`.

### The metric has to stay honest

When builtin classes became harvestable, `new ReflectionClass` began succeeding
into an object with no behaviour, which produces plausible-but-wrong output
instead of an honest gap. Instantiation is deliberately restricted to classes
the runtime actually models, and that cost a passing test on purpose.

`php2phpt` reports `passed`, `failed`, `unsupported`, and `errored` separately
for the same reason. `failed` rising while `unsupported` falls is the expected
shape of progress: tests that used to stop at a missing opcode now run to
completion and diverge somewhere later. The numbers only make sense read
together, and `errored` — a Go error escaping the harness — should trend to
zero.

## Files

- `runtime/interp/throw.go` — `throwError` and the engine error classes.
- `runtime/interp/class.go` — `isBuiltinOnly`.
- `runtime/interp/interp.go` — the instantiation restriction and the
  undefined-function / undefined-method paths.
- `cmd/php2phpt/main.go` — outcome classification, `asUnsupported`.

## Test coverage

`runtime/interp/interp_test.go` asserts that an unsupported opcode is reported
as an `UnsupportedError` rather than as output;
`runtime/interp/differential_test.go` fixes the exact wording of every error
message against the engine.

## Pitfalls

- Never widen a capability in a way that turns a gap into wrong output to raise
  the pass rate. That trade is always available and always a loss.
- Do not guess which side of the table a case falls on. Ask `Manifest.builtins`,
  ask `isBuiltinOnly`, ask whether the program declared the class.
- Message wording is settled by the engine, not by memory. See
  [differential-oracle](./differential-oracle.md).
