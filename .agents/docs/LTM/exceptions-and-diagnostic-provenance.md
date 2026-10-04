# Exceptions and Diagnostic Provenance

## Summary

PHP-visible failures have a kind, message, source position, stack and control-
flow effect. Matching only the text is insufficient: a warning can invoke a
handler, a thrown `Error` can be caught, and an unsupported runtime capability
must remain a Go-side gap rather than masquerading as either.

The durable rule is to construct diagnostics where the semantic decision is
made, capture provenance at the raising instruction, and route the resulting
PHP throwable through the same region/unwind machinery as a user `throw`.

## Three failure domains

| Domain | Representation | Observable behavior |
|---|---|---|
| PHP diagnostic | `Ctx` warning/deprecation path | respects mask and user handler |
| PHP throwable | `phpError`/Throwable through `f.raise` | catchable, traced and unwound |
| php2go gap | `UnsupportedError`/Go error | classified as unsupported or errored |

A program error must not be returned as an ordinary Go error: that makes a
valid PHP `catch` ineffective. A missing implementation must not be turned into
a PHP error with plausible wording: that changes the program rather than
reporting the compiler/runtime limit.

## Raising position and stack capture

The stack is attached at the instruction that raises. Waiting until a handler
or materialization point loses frames already popped, especially magic methods,
property hooks, callbacks and constructors.

The current frame and the reporting location are separate:

- a call-site error names the caller's file/line;
- a method frame names the class that declared the body;
- a hook frame uses `Class->$property::get/set`;
- an autoloader or magic call temporarily moves the active position;
- a compile-time diagnostic can be stored during harvest and replayed when the
  declaration instruction executes.

PHP quoting is its own renderer. `%q` escapes bytes as Go does; PHP often wraps
the original bytes and does not escape namespace separators or embedded
characters the same way.

## User error handlers

Warnings and deprecations go through one `Ctx` path. The handler is temporarily
removed while it runs so diagnostics inside it do not recursively call the same
handler. A handler that throws interrupts the raising opcode before that opcode
continues to a later refusal or mutation.

`@` narrows the current error mask to PHP's fatal subset. It does not replace
the mask with a fixed constant, and the mask is execution-context state that
must follow fibers.

When a callback throws and the lower layer cannot immediately return it, the
exception is parked on the context and collected at the next explicit boundary.
Every driver loop that invokes PHP code must propagate non-PHP errors as well;
discarding them turns gaps into partial output.

## Exceptions, finally and chaining

Throwing enters the innermost matching region. A catch that does not match
continues searching; it is not the end of the frame. Catch assignment writes
the exception into the named slot using ordinary assignment semantics.

When a finally throws while another exception is active, the new throwable is
the one propagated and the old throwable becomes its `previous` chain. A
return can deliberately discard the active exception. These are control-flow
operations and must agree between normal interpreter execution and cloned
finally paths in generated code.

Uncaught rendering includes the throwable's chain and exact frame names. The
exception trace roots its argument values while local slots are released; it
does not necessarily keep the receiver alive.

## Compile and link diagnostics

The Zend compiler can emit output even when compilation succeeds, and it can
attach warnings/fatals to declarations that only become active at run time.
The harvester records those diagnostics; the runtime replays them at the same
declaration boundary rather than printing everything during harvest or dropping
it.

Link-time class errors are PHP failures. Missing parent/interface/trait,
signature compatibility, trait conflict and enum validation can run after prior
program output and may involve autoloading before the final message.

## Relevant files

- `runtime/php/diagnostics.go`, `throwable.go`, `ctx.go` — shared errors,
  warnings and provenance state.
- `runtime/interp/errorhandler.go`, `throw.go`, `interp.go` — routing, stacks
  and regions.
- `internal/gogen/gogen.go` — generated on-error paths and source tracking.
- `internal/zend/zend.go` — compile diagnostic harvest.

## Test coverage

`runtime/interp/differential_test.go` covers PHP-visible behavior;
`runtime/php/diagnostics_test.go` and `throwable_test.go` cover the value model.
`cmd/php2gotest/testdata` contains uncaught, handler, stack-frame, finally and
exception-chain cases. Request shutdown failures belong in
`testdata/lifetime`.

## Pitfalls

- A diagnostic with the right words but the wrong kind is a control-flow bug.
- Capture the stack before re-entrant frames unwind.
- Restore source position after an autoloader or callback before raising the
  caller's error.
- Inspect `errored` separately from the failing set; an escaped Go error may
  leave visible output that looks deceptively close.
