# Project Overview

php2go runs PHP on Go by reusing the real Zend compiler instead of
reimplementing it. The engine is linked in through the embed SAPI, asked to
compile a script, and its `zend_op_array`s are harvested into a serialized IR
that a Go runtime then executes. Nothing in the pipeline parses PHP itself.

Go owns `main`, so this is deliberately not a PHP extension: the Go runtime
starts normally rather than at `dlopen` time.

## Pipeline

1. `internal/zend` drives the linked engine over cgo — compile a file or string,
   read the op arrays, classes, constants, builtin signatures, and build layout
   out of engine memory, and evaluate PHP for the differential oracle. Every
   entry point that can bail is wrapped in `zend_try`, because a Zend `longjmp`
   must never unwind across the cgo boundary.
2. `internal/ir` converts those engine-memory structures into the on-disk IR
   (`proto/php2go/ir/v1/ir.proto`, generated into `internal/ir/irpb`) and back.
   The `Manifest` records provenance — PHP version, operand layout, opcode
   names, builtins, constants, builtin classes — because IR is only meaningful
   next to the build that produced it.
3. `runtime/php` is the value model: zvals, arrays, objects, references,
   operators, and the `Ctx` that carries output and diagnostics.
4. `runtime/interp` executes the IR: calls, classes, closures, generators,
   enums, magic accessors, autoloading, includes, and exception handling.
5. `internal/bundlebuild` inventories a project, harvests every PHP source and
   generates a Go module; `runtime/bundle` reads that module's embedded,
   content-addressed sources and units without linking Zend by default. An
   optional `php2go_hybrid` build tag links Zend through `runtime/hybrid` to
   compile runtime eval strings for the Go interpreter. `internal/userland`
   catalogs embedded PHP bodies for selected engine builtins. See
   [BUNDLING.md](./BUNDLING.md).
   The same tag on `php2gotest -emit` modules compiles previously unseen eval
   strings and runs only their IR bodies through the interpreter, copying
   lowered variable slots into and out of the eval frame.
6. `internal/phpt` parses `.phpt` files, including `EXPECTF` placeholders and
   skip conditions.

## Commands

| Command | Purpose |
|---------|---------|
| `cmd/php2go` | Driver for the pipeline; `dump` harvests IR, `bundle` packages a project into a `go:embed` application, and `profile` records eval strings from a reference run. |
| `cmd/php2harvest` | Compiles scripts and reports op arrays; `-layout` and `-builtins` report the linked build. |
| `cmd/php2eval` | Runs a snippet through the engine and the interpreter side by side. The probe used to settle behaviour. |
| `cmd/php2phpt` | Grades a `.phpt` corpus; `-diff N` prints expected against actual, `-diff-kind` chooses the bucket. |

## Invariants

- **Harvest, never transcribe.** Opcode names, builtin signatures, constants,
  and the builtin class hierarchy all come from the linked build. They are
  version-specific.
- **Zend operand fields are engine-memory encodings.** Literals, CV/TMP/VAR
  slots, and jump targets are byte offsets; `*_MASK` flags are enumerated
  fields, not bitmasks. These serialize into plausible-but-wrong IR when
  misread.
- **A gap is not an error.** An interpreter gap is an `UnsupportedError` counted
  separately; a program's mistake is a thrown PHP error with the exact engine
  wording. See [QUALITY_GATE.md](./QUALITY_GATE.md).
- **A class entry's tables are flattened.** `function_table` and
  `properties_info` include what was inherited; filter methods by
  `common.scope` and order properties by `offset`.
- **The harness must be as faithful as the interpreter.** `php2phpt` reproduces
  what `run-tests.php` does — the source file's name and location, and trimming
  both ends before comparing — because it has been the wrong party twice. See
  [LTM/phpt-harness-fidelity](./LTM/phpt-harness-fidelity.md).

Read [TESTING.md](./TESTING.md) before changing the harvester or grading
anything, and [QUALITY_GATE.md](./QUALITY_GATE.md) before declaring work
complete.
