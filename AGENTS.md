# Documents for coding agents

- [Project overview](./.agents/docs/OVERVIEW.md)
- [Development journal](./.agents/docs/JOURNAL.md)
- [Long-term memory index](./.agents/docs/LTM/INDEX.md)
- [Open work](./.agents/docs/TODO.md)
- [Testing guide](./.agents/docs/TESTING.md)
- [Quality gate](./.agents/docs/QUALITY_GATE.md)

## General

php2go compiles PHP to Go by harvesting the Zend compiler's own output through
the linked engine, then executing that IR in a Go runtime. Read the overview
before changing the harvester, the IR, or the interpreter, and read the testing
guide before trusting any measurement.

The rule the project is built on: **ask the engine, do not reason from the
headers.** Behaviour is settled by a differential case or a `php2eval` probe,
never by reading `zend_vm_def.h` and inferring. Reasoning about Zend operand
encodings has been wrong more often than right.

## File management

- Put agent notes under `.agents/docs/`.
- Put temporary artifacts under `.agents-workspace/tmp/`, never in the tracked
  source tree. Throwaway clustering scripts and `-diff` dumps belong there.
- `third_party/php-src` is a pristine submodule. Do not modify it; generated
  build files live in `third_party/php.BUILD.bazel` and Bazel outputs.
- Do not delete files you did not create in the current session without
  permission.

## Building and testing

- Building anything that links the engine needs the CGO environment:

  ```sh
  eval "$(hack/cgoenv.sh)" && go build ./...
  ```

  See [TESTING.md](./.agents/docs/TESTING.md) for the `BAZEL` and
  `BAZEL_STARTUP` variables it depends on.

- Run `gofmt -w` on every changed Go file.
- The standard gate for Go changes is:

  ```sh
  gofmt -l <changed-go-files>
  go build ./...
  go vet ./...
  go test ./...
  ```

- Add a differential case for every behavioural change. Hand-written expected
  output only tests what the author believed PHP does.
- Keep Bazel `BUILD.bazel` files in step with new Go files and testdata.

## Measurement

- `.phpt` outcomes are `passed` / `failed` / `unsupported` / `errored`, and the
  distinction is load-bearing. Never let a gap masquerade as working behaviour to
  raise the pass rate; report `unsupported` instead.
- Quote before-and-after counts from the same corpus and command when claiming
  progress. `failed` rising while `unsupported` falls is normal and must be read
  together.

## Git workflow

- Do not use `git checkout` or `git restore` on a shared working tree.
- Do not commit or push unless the user explicitly asks.
- Preserve unrelated existing changes.

## Documentation

- Add findings to `.agents/docs/JOURNAL.md`, newest entry first; do not rewrite
  existing entries except through the explicit `reconcile-journal-ltm` workflow.
- Keep durable, topic-oriented knowledge under `.agents/docs/LTM/` and
  unresolved work in `.agents/docs/TODO.md`.
