# Quality Gates and Corpus Measurement

## Summary

php2go has two correctness surfaces: engine differentials for behavior and
corpus grading for reach. Both can lie when their inputs, state or outcome
buckets differ. A trustworthy claim names the command and corpus, reports every
bucket, and is reproduced from an unchanged tree.

## Corpus outcomes

`.phpt` grading has distinct outcomes:

| Outcome | Meaning |
|---|---|
| `passed` | compiled/executed and matched |
| `failed` | executed and produced different PHP-visible behavior |
| `unsupported` | php2go honestly declared a capability gap |
| `errored` | harness/compiler/runtime failure escaped classification |
| `compile-error` | the oracle/input did not compile for this measurement |

`failed` rising while `unsupported` falls is normal: more programs are being
graded on their next real blocker. The pass rate used by the project is
`passed / (passed + failed)`; quote the numerator, denominator and all outcome
counts rather than carrying a percentage forward by eye.

Before/after claims use the same binary, corpus, command and options. Read
`compile-error` first. It is load-sensitive on the shared machine and changes
the attempted denominator, so a non-zero run is noise and must be retaken.

## Test coverage

Expected output comes from the linked engine at test time. A handwritten
expected string only proves the author's belief. The two runs must have equal
state and capabilities: reset engine globals/caches/registries, install the
same loaders and builtins, and use the same filename/SAPI conventions.

The lowerer golden and executable differential fixture sets intentionally stay
separate. Goldens want small, output-free examples and pin shape; executable
fixtures must print and use the value whose behavior they claim to test. A new
construct normally needs both.

Request-shutdown behavior cannot be observed through repeated eval and lives in
the lifetime harness. A probe binary must be rebuilt after runtime changes;
`go build ./...` does not refresh an explicitly named old binary.

## The Go gate

Run `hack/gate.sh` from any working directory. It anchors to the repository root
and accumulates the status of formatting, build, vet and tests before printing a
verdict. A success banner placed after a pipeline can report success for the
last command rather than the checks shown above it.

The verdicts distinguish:

| Verdict | Meaning |
|---|---|
| `GREEN` | the checks passed for the captured input set |
| `RED` | the failure reproduced with unchanged inputs |
| `UNSTABLE` | source or gate changed during the run; not a result |
| `FLAKY` | the verdict changed with unchanged inputs |

Only tests are nondeterministic among the standard Go checks. Use repeated test
counts before a high-confidence claim when flakiness matters; the default stays
fast and means “passed once”.

## Shared-tree measurement

Generated programs import `runtime/php` from the live working tree. Editing a
shared API while `php2gotest` runs gives a frozen emitter and a changing
runtime; failures then begin at an alphabetical point in the corpus. Stop
editing during the run or measure from a copied tree under
`.agents-workspace/tmp/`.

A single RED in a shared tree is an observation, not attribution. Re-run it and
use input checksums to decide whether the tree moved or the check is flaky.
Announce shared API changes before starting them so another session does not
take a measurement against an incompatible midpoint.

## Census and clustering

The census is a second implementation of emitter reachability and can drift.
It must walk file bodies, functions, static and instance methods, closures and
property hooks with the same initial context as emission. Before acting on a
head, verify a named file: a census-reported gap in a file that graded as
`failed` rather than `unsupported` is evidence that the census is wrong.

Rank work by sole blockers or by the unmatched suffix after the first divergent
line, not merely by opcode frequency or unified-diff size. A refusal label names
where compilation stopped and may not name the missing feature. Keep temporary
diffs and clustering scripts in `.agents-workspace/tmp/`.

## Determinism and negative checks

A passing check is trustworthy only when its failure mode is demonstrated.
Mutation tests caught guards that ran only on the happy path, checks that passed
vacuously, and emitted comments claimed to be invalid Go that were actually
valid.

Compiler output must be deterministic. Map iteration cannot decide instruction
numbers, block parameters, table order or textual output without sorting.

Never revert a probe with a git command that overwrites the working tree. This
repository may have no useful committed/indexed baseline; restore the explicit
backup made for the probe.

## Relevant files

- `hack/gate.sh`, `hack/cgoenv.sh` — Go quality gate and engine environment.
- `cmd/php2phpt`, `internal/phpt` — `.phpt` grading.
- `cmd/php2gotest` — compiled-path corpus and census.
- `runtime/interp/differential_test.go`, `runtime/php/engine_parity_test.go` —
  engine-derived behavior.
- `.agents/docs/TESTING.md`, `QUALITY_GATE.md` — current operational policy.

## Pitfalls

- Do not quote a pass count without checking the other buckets.
- Do not trust a census or percentage copied from an earlier round.
- Do not call a hang `unsupported`; it is an errored execution.
- Do not let a gap render as valid output merely because a later stage was
  expected to reject it.

## Reproducible coordination and source paths

A correction channel needs an acknowledgement that distinguishes delivery from
text merely appearing on screen. With a TUI in another `tmux` pane,
`send-keys '<text>' Enter` can race paste handling and leave the text unsent in
the input box. A `capture-pane` match is not proof of delivery because it sees
that unsent input too. Send literal text with `send-keys -l`, press Enter, and
confirm that the text has disappeared from the input box; retry or fail loudly
when it has not. This is the coordination form of the ordinary rule that a
guard is evidence only after its failure mode has been demonstrated.

Generated artifacts must also be independent of the build machine's directory
layout. Zend can place an absolute compile-time source path in a harvested
diagnostic, while the oracle reports the name it was given. Normalize both arms
to compilation-root-relative paths with `php2gotest -root` and
`gogen.TrimBuildPath`; the generated binary's own path is unrelated to the PHP
source location. `TestNoBuildMachinePathReachesTheArtifact` is the negative
check: emitted programs contain no absolute build path.
