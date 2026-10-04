# .phpt Harness Fidelity

## Summary

`php2phpt` has to reproduce what `run-tests.php` does, not merely something
reasonable. Three separate infidelities have been found, and each looked like
hundreds of unrelated interpreter bugs: together they were worth roughly 850
tests. Before attributing a `.phpt` failure to the runtime, read the relevant
part of `third_party/php-src/run-tests.php`.

## Key facts

| What `run-tests.php` does | Where | Why it matters |
|---|---|---|
| Writes `--FILE--` to `<test-dir>/<test-name>.php` | `$main_file_name = basename($file, 'phpt')`, ~line 1955 | An `include` resolves against the including file's directory, and hundreds of expectations name the file — `in %sfoo.php on line 3` |
| Compares `trim($out)` against `trim($wanted)` | ~line 2538 and ~2593 | The engine prints a **leading** newline before every fatal error; trimming only the tail fails every single-diagnostic test |
| Normalizes `\r\n` to `\n` on both sides | same | A heredoc body carrying CRLF otherwise fails against an LF expectation, and the two are **indistinguishable in a printed diff** — even `cat -A` shows the same thing. Implemented 2026-08-24, having been listed here unimplemented since the table was written |
| Excludes nothing from the denominator itself | — | php2go excludes `compile-error` and `skipped`; quote the denominator with the rate |
| Prints compile-time diagnostics even when the compile **succeeds** | — | `"${name}"` interpolation and implementing `Serializable` are deprecated at compile time; that text precedes the program's own output |

## Details

### The file must be named after the test

`foo.phpt` becomes `foo.php` **beside it**, not in a temporary directory and not
under a random name. Both halves matter and they fail differently:

- Wrong *directory*: tests requiring a sibling `.inc` fail, because PHP resolves
  an include against the directory of the file doing the including.
- Wrong *name*: every `EXPECTF` mentioning the file fails. `%s` matches any
  non-newline run, so `%sfoo.php` matches `/long/path/foo.php` but never
  `/long/path/php2phpt-1234.php`. This was worth ~600 tests and did not look
  like a naming problem at all — it looked like hundreds of separate
  diagnostics being subtly wrong.

`php2phpt` refuses rather than overwrites when the target `.php` already exists,
and removes it afterwards. The submodule should be clean after a run; check
`git status` inside `third_party/php-src` if in doubt.

### Trimming is symmetric

PHP's `trim()` cutset is `" \t\n\r\0\x0B"`, applied to both the output and the
expectation. The engine emits `"\nFatal error: ..."` — the leading newline is
PHP's own, not a separator — so a right-trim-only comparison fails every test
whose entire expected output is one diagnostic.

### Compile errors are a bucket, not a failure

Many tests exist precisely to trigger a compile error, so `outcomeCompileError`
is neither a pass nor an interpreter failure and is excluded from the attempted
denominator. That makes the pass *rate* incomparable across runs where the
bucket moved: after compile diagnostics started being reported, the bucket went
710 → 1 and the denominator moved 3860 → 4569. Always quote both numbers.

`php2phpt -diff N -diff-kind compile` dumps that bucket; `-diff-kind errored`
dumps the Go-error one. A wall of compile errors almost always means the harness
is feeding the compiler something `run-tests.php` would not have.

### A successful compile can still print

`CompileFile` used its captured output only when the compile failed. A compile
that succeeds can print too, and the text is the program's output. It now rides
on the op_array and through the IR, so a serialized unit keeps it.

The differential oracle cannot see this: `zend_eval_string` does not emit
compile-time deprecations the way `zend_compile_file` does, so the engine side
of a `php2eval` comparison prints nothing. Verify such behaviour against a real
`.phpt` instead.

### Sections that are not honoured are skipped, not ignored

`--SKIPIF--`, `--INI--`, `--EXTENSIONS--` and `--CLEAN--` all mean the test
cannot be attempted faithfully yet, and `SkipReason` says which. Running such a
test anyway would let a gap masquerade as a pass.

## Files

- `cmd/php2phpt/main.go` — `runOne` writes the source file; `wantsDiff` selects
  the bucket `-diff` draws from.
- `internal/phpt/phpt.go` — section parsing, `phpTrimCutset`, `Matches`, the
  `EXPECTF` translation, `SkipReason`.
- `third_party/php-src/run-tests.php` — the authority. Read it.

## Test coverage

`internal/phpt/phpt_test.go` covers parsing and matching.
`cmd/php2phpt/lifetime_test.go` covers request-shutdown behaviour the
differential oracle cannot observe.

## Pitfalls

- A harness bug does not look like a harness bug. It looks like a large,
  incoherent set of interpreter bugs. When a cluster refuses to resolve into a
  single rule, suspect the harness.
- The `%`-placeholder translation is not regex-equivalent to PHP's for every
  code; `%s` is `[^\r\n]+` and `%a` is `.+`, and confusing them makes a
  clustering script report false differences.

## Resource ids depend on the SAPI

The first resource a script opens is **#5** under the CLI and **#2** under
embed. The `.phpt` corpus was recorded with the CLI — 16 places assert
`resource(5)` — while the differential oracle runs the embed SAPI. php2go
numbers user resources from 5 (`firstScriptResource`) so the corpus matches;
`STDIN`, `STDOUT` and `STDERR` take 1, 2 and 3, and 4 is whatever else the CLI
holds open before the script starts.

The consequence for differential cases: **never print a resource id**. The two
sides will disagree, and the disagreement is the harness, not the runtime.

## A test can write into the submodule

`third_party/php-src` is pristine, and a test that writes a file beside itself
normally unlinks it again — but a run that stops early (an unsupported call, a
fatal) never reaches that line. `bug52508.phpt` left `Zend/tests/bug52508.ini`
behind the moment `file_put_contents` started working.

`php2phpt` snapshots each test's directory and removes plain files that appeared
while it ran. Check `git -C third_party/php-src status` after a corpus run
anyway: the guard only covers the directory the test lives in.

## ob_get_level() is relative too

`php2go_eval` captures the engine's output through `php_output_start_default()`,
so a script that reports `ob_get_level() == 1` under the CLI reports **2** under
the oracle. Differential cases must compare the level *relative* to
`ob_get_level()` taken at the top — the same shape as the resource-id problem
above.

The corpus is unaffected: `php2phpt` runs the interpreter directly, with no
buffer of its own.

## Read the counts before trusting a set difference

`php2phpt -diff` dumps only the **failure** bucket, so comparing two dumps
cannot distinguish `passing -> failed` from `unsupported -> failed`. A change
that unblocks tests therefore shows them as "newly failing", which reads like a
regression and is not one.

The three counts settle it immediately: if `unsupported` fell by the same number
that `failed` and `passed` rose by, nothing regressed. Check that before
bisecting — 2026-08-25 lost half a round to reverting changes one at a time,
looking for the cause of three tests that had never passed.

## Eval'd code is named after the site, not the file it was compiled from

The engine calls code that `eval` compiled `<file>(<line>) : eval()'d code`,
where the file and line are the *eval's own*. That name is what appears in every
diagnostic raised inside it, in a stack trace, and in `__FILE__`.

php2go has no compiler: `Loader.CompileString` hands the source to the harvester,
which writes a temporary file and compiles that, so the whole unit -- every
op_array's `filename`, every class's, and the `__FILE__` **literal the compiler
folded in** -- names the temporary. `renameCompiled` (`runtime/interp/include.go`)
rewrites all of them, matching literals by exact equality with the old name.

Two consequences worth remembering:

- `__FILE__` is a compile-time literal, not something read from the op_array at
  run time. Renaming an op_array is not enough to change what it prints.
- `__DIR__` is folded as the dirname of the temporary, and the eval's *name* is
  a sentence rather than a path, so the directory has to be passed in
  separately. It is the directory of the file that ran the eval.
- The fold is not always the whole literal: `__DIR__ . "\n"` is one literal, so
  the rewrite has to be by substring. That is safe only because the harvester's
  temporary path is unique per compile.
- An anonymous class is named `class@anonymous<file>:<line>$<hash>`, so the
  *class's own name* carries the path too, and the opline that declares it holds
  that name as a literal. Rewrite one without the other and the declaration
  cannot find its class.

Nothing about this is observable through the differential oracle without care:
the oracle runs the case through the engine's own `eval`, so its outer file is
`php2go-eval` while the interpreter's is a real temporary. Compare the tail
(`substr($f, strpos($f, ") : ") + 1)`), never the whole name.

## A `#!` first line is the SAPI's to skip, not the compiler's

`CG(skip_shebang)` is a compiler global. Every SAPI sets it before compiling the
file the request starts from, and `zend_compile_file` never sets it itself --
so an *included* file beginning `#!php` echoes that line as inline HTML while
the primary script does not.

A harness that compiles everything through one path gets this wrong in one
direction or the other. php2go splits it: `Engine.CompileMainUnit` for the
script the request starts from, `Engine.CompileUnit` for an include, and
php2phpt uses the first for the test's own `--FILE--`.

The symptom is a stray first line of output that no PHP construct accounts for.
