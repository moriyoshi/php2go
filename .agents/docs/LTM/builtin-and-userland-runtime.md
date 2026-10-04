# Builtins and the Userland Runtime

## Summary

The public builtin surface is closed by the linked engine, while implementation
is split between native Go leaves and PHP bodies harvested into the bundle. A
userland replacement keeps the engine function's public identity and signature;
only its body comes from PHP.

The guiding boundary is the smallest capability the PHP implementation cannot
express. Parsing, validation, result shaping and diagnostics remain PHP; host
I/O, resource ownership or process state cross a narrow native leaf.

## One accountable surface

`internal/userland/surface.tsv` is generated from php-src stubs, runtime native
registrations and the userland catalog. Each function is exactly one of:

| Disposition | Meaning |
|---|---|
| `native` | implemented by the runtime dispatcher |
| `userland` | a harvested PHP body is installed behind the public name |
| `unavailable` | absent from this linked PHP build |
| `pending` | recognized but not yet implemented; the completed ledger has none |

The ledger is generated, so it is current by construction and the split moves
as functions migrate. At 2026-09-15 it reads **260 native, 274 userland and 15
unavailable**: all 534 functions available in the linked build are classified as
an implementation, and none is `pending`. This is a surface-coverage statement,
not proof of behavioral parity for every input.

**Quote the file, not this paragraph.** The completed 2026-09-02 ledger read
272 native / 262 userland; twelve functions have crossed native to userland
since, `print_r` among them, and the counts here will go stale the same way.
`TestSurfaceClassification` pins the **total** at 549 and `unavailable` at 15 --
so a migration keeps the tests green while any transcribed native/userland split
rots silently. The split is a reading of `surface.tsv`, never a number carried
forward.

Availability is harvested, never transcribed from headers or stubs. Platform
and compile-time exclusions belong in `unavailable`; they must not be hidden to
improve the ratio.

## Installing a PHP builtin body

`internal/userland` embeds PHP sources plus explicit public-name-to-helper
aliases. The bundler harvests them under reserved virtual paths. At runtime:

- helper declarations live in a separate hidden table;
- only catalogued aliases are exposed;
- the public call retains the engine manifest's parameter names, defaults,
  by-reference flags and internal-function identity;
- the harvested op array supplies the implementation body;
- request state is reset along the same boundary as native builtins.

This is function-only. It is not a native extension ABI, does not execute a PHP
bootstrap parser, and does not make internal classes or resources appear merely
because a function body names them.

## Choosing the boundary

The successful pattern is a PHP composite over one exact leaf:

- byte grammars such as CSV, pack/unpack, INI, URLs, source highlighting and
  image headers stay in PHP;
- stream functions validate and shape results in PHP after a native operation
  on an already-valid resource;
- filesystem composites use loader-aware byte/stat/path leaves;
- sorting with a PHP comparator uses a callback-capable ordering leaf while
  key policy and result construction stay in PHP;
- state facades can be explicit no-ops or fixed values when the embed SAPI
  genuinely has no underlying state.

Do not create a broad host abstraction because one public function needs one
bit of information. The repeated result of the surface work was that a narrowly
named leaf supported several PHP composites without leaking a second runtime
model into userland.

## Reaching userland from compiled output

A PHP body installed behind a public name is reachable by the interpreter
because the interpreter has the harvested op array. **A compiled program does
not**, so the library has to be linked into the emitted unit:
`cmd/php2gotest/userlandlink.go` loads the catalog, lowers the bodies a program
actually calls -- following `call.fn` operands and `fallback=` attributes, and
collecting their closures -- and emits them alongside the program's own
functions.

This is not optional polish. Moving a function from native Go to a PHP body
takes it *away* from the compiled back-end until the linker can reach it: the
`print_r` migration cost 29 passing corpus files on its own and repaid 34 once
the library was linked. The disposition ledger is one surface for two back-ends,
and a migration is only half done while one of them cannot see the body.

The linker **refuses honestly** rather than emitting a body it cannot complete.
`internal/userland/shape.tsv` is the generated list of the 74 `PHP2Go\Internal\*`
leaves the PHP bodies call; a body whose leaf the compiled runtime lacks is
reported as `userland leaf X is not in the compiled runtime` and the file is
`unsupported`. Four leaves were outstanding at 2026-09-15:
`directory_operation`, `register_superglobal`, `path_stat`, `append_file`.

## Calls from shared runtime code

`runtime/php` cannot resolve a program callable, class or iterator on its own.
It declares hooks on `Ctx` such as call-value, traversable and object-property
resolvers; the interpreter or generated unit supplies them. This is dependency
inversion, not dynamic discovery: the lower layer states the capability and the
program-owning layer installs it.

A stored callback is validated when the public API requires it, retained in
request state, and invoked later with its own observable frame. Error handlers,
shutdown functions, output handlers, tick functions and stream callbacks share
that lifecycle shape but not necessarily identical validation rules.

## Diagnostics from hidden bodies

Hidden library helpers must raise PHP's ordinary diagnostic levels, not the
`E_USER_*` levels emitted by `trigger_error`. Reserved warning/deprecation
primitives carry the message through `Ctx` and attribute it to the public
caller, outside hidden frames.

The public signature still runs before the body, so arity, named-argument and
by-reference errors use the engine's harvested contract even when the
implementation is PHP.

## Relevant files

- `internal/userland/catalog.go`, `ext/standard.php`, `ext/streams.php` — PHP
  library catalog and bodies.
- `internal/userland/surface.tsv`, `cmd/surfacegen` — generated disposition
  ledger.
- `internal/userland/shape.tsv`, `cmd/shapegen` — generated list of the
  `PHP2Go\Internal\*` leaves the PHP bodies call.
- `runtime/interp/userland.go`, `builtins.go` — installation and dispatch.
- `cmd/php2gotest/userlandlink.go` — linking the same bodies into compiled
  output.
- `runtime/php/builtin.go`, `callable.go`, `ctx.go` — shared signatures and
  capability hooks.
- `.agents/docs/BUNDLING.md` — current user-facing inventory and bundle model.

## Test coverage

`internal/userland/surface_test.go` checks completeness, sort order, source
drift and catalog consistency, and `TestSurfaceAvailabilityMatchesLinkedEngine`
checks the ledger against the build actually linked. What it does **not** check
is the native/userland split -- see above. Each implementation needs an engine
differential for its public behavior. Bundle tests verify that userland bodies
run without Zend and keep engine signatures.

## Pitfalls

- A complete disposition table is not a correctness score.
- Do not implement a public stream operation in PHP by manufacturing a second
  resource model.
- A native registration may still be transitional; classification follows the
  smallest truthful boundary, not implementation language preference.
- A migration to a PHP body is not finished when the interpreter passes. Check
  the compiled back-end in the same measurement, or the move shows up as a
  corpus regression with no obvious cause.
- Generated tables containing program callbacks cannot be initialized as Go
  package literals when that creates an initialization cycle.
