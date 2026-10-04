# Testing Guide

## Build environment

Everything that links the engine needs the CGO flags derived from the Bazel
output tree. `bazel` is not on `PATH`, and `hack/cgoenv.sh` needs both the
binary and the output root that produced `bazel-bin`:

```sh
export BAZEL=~/.cache/bazelisk/downloads/sha256/<hash>/bin/bazel
export BAZEL_STARTUP="--output_user_root=<the root bazel-bin points into>"
eval "$(hack/cgoenv.sh)" && go build ./...
```

If `cgoenv.sh` reports a missing generated tree, build the engine first:
`$BAZEL $BAZEL_STARTUP build @php//:sapi_embed_shared`.

Regenerating the IR bindings after a `.proto` change needs `buf` and
`protoc-gen-go`: `(cd proto && buf generate)`. The generated `.pb.go` is
committed, so an ordinary build needs neither tool.

## Run the userland probe before measuring a lowering or emitter change

`runtime/interp/differential_test.go` holds ~320 single-line cases for the
functions reimplemented in PHP under `internal/userland`. They run
**interpreter-only**. Extracted into `.agents-workspace/tmp/ulprobe` and run
through `php2gotest`, they become a differential for the **compiler** over
several thousand lines of real PHP that no `.phpt` covers:

    python3 - <<'EOF'
    import os, shutil
    out='.agents-workspace/tmp/ulprobe'
    shutil.rmtree(out, ignore_errors=True); os.makedirs(out)
    lines=open('runtime/interp/differential_test.go').read().split('\n')
    start=next(i for i,l in enumerate(lines)
               if 'The batch reimplemented in PHP under internal/userland' in l)
    n=0
    for l in lines[start:]:
        t=l.strip()
        if not (t.startswith('`') and t.endswith('`,')): continue
        body=t[1:-2]
        if '`' in body: continue
        open(os.path.join(out,'c%04d.php'%n),'w').write('<?php\n'+body+'\n')
        n+=1
    EOF

It takes about a minute to build and ten to run, and it catches a class the
fixture suite structurally cannot: **a fault inside the linked library**. The
generated program contains the userland PHP, so a bad emission there is a build
failure in code no test file mentions.

That is not hypothetical. The undecided-send change (2026-09-20) passed
`go test ./...` and all 303 fixtures, and broke **seven** corpus files with
`cannot use p_x_c0 (variable of struct type place) as php.Value` -- inside
`parse_ini_string`. The probe would have shown it in ten minutes instead of a
45-minute corpus run plus a tally.

**Order to run things in** for a change to `internal/lower` or `internal/gogen`:

1. `gofmt -l`, `go build ./...`, `go vet ./...`, `go test ./...`
2. the fixture suite
3. **the userland probe**
4. only then the corpus

## A detached corpus run needs the whole CGO environment, not `cgoenv.sh`

`hack/cgoenv.sh` reads `$BAZEL` and `$BAZEL_STARTUP`. An interactive shell here
has them because the scratchpad `env.sh` exports them *and then* evals cgoenv; a
`nohup setsid` script that evals cgoenv alone gets

    cgoenv: could not determine bazel-bin; run a build first

and its `go build` fails on `sapi/embed/php_embed.h: No such file or directory`
about a second after launch. A detached runner must source `env.sh`.

**How this wasted a run:** the launcher reported a pid, the chunk outputs were
absent (which reads as "not finished yet"), and the liveness check said the
runner was alive. Twenty minutes of work went past before anyone looked at the
log. Two rules come out of it:

- **A run that has produced no output file is not "early", it is suspect.** The
  redirect creates each chunk file at once, so zero files a minute in means the
  run never got past its build. Have the runner `touch` a `.failed` marker on a
  build failure and watch for that as well as for `.done`.
- **`pgrep -f <script>` matches the watcher itself.** The monitor's own shell
  command contains the script's name, so the pattern finds it and the check
  reports "alive" forever, including over a corpse. Match the **worker binary**
  with `pgrep -x`, and corroborate with how many chunks have reached their
  `files ` summary -- a process count alone cannot tell "finished" from "died".
  A single missing-worker reading is also not death: it lands in the gap
  between chunks. Require the condition to hold several checks running.

Silence from a background run is not evidence that it is running.

**The same self-match kills, not just reports.** `pkill -f 'run-zy2.sh'` in a
shell whose own command line contains `run-zy2.sh` kills that shell -- so the
compound command died at the `pkill` and never reached the relaunch after it,
while the run it meant to stop carried on. Symptom: an exit code in the 140s and
a "restart" that did nothing. Write the pattern so it cannot match itself:

    pkill -f 'run-z[y]2\.sh'

The bracket makes the *pattern* text differ from the text it matches. Then stop
the workers by exact name (`pkill -x php2gotest.bin`), confirm both counts are
zero, and delete the half-written chunk files before relaunching -- a chunk
killed mid-run keeps its partial output and has no `files ` summary, which is
precisely the shape `tally.sh` refuses.

## The fixture suite no longer fits in one foreground command

`cmd/php2gotest/testdata` is past 300 files and takes over ten minutes, which is
the Bash tool's hard ceiling -- a longer `timeout:` is silently clamped, so the
command is killed mid-run and reports nothing. Run it detached and wait on a
marker, the same shape the corpus runs use:

    #!/bin/sh
    . "$S/env.sh"
    cd /home/moriyoshi/src/php2go
    rm -f "$S/fix.done"
    go run ./cmd/php2gotest -php2eval "$S/php2eval" -v cmd/php2gotest/testdata > "$S/fix.out" 2>&1
    touch "$S/fix.done"

`nohup setsid` it, then poll for `fix.done`. A killed foreground run looks
exactly like a hang, and re-running it "to be sure" costs another ten minutes.

## Any pattern-based process match can kill the shell running it

The recorded trap is `pkill -f 'run-zy2.sh'` matching its own command line, and
the bracket trick (`run-z[y]2\.sh`) is the recorded fix. Both halves are
narrower than the problem. Hit twice on 2026-09-25, each time ending the shell
mid-command with exit code 144:

- **The bracket trick does not help when the same command also writes the file.**
  `pkill -f 'es[c].sh'` in a command whose heredoc writes `esc.sh` matches the
  shell's own arguments through the heredoc text. The script was left truncated
  at 294 bytes and "launched" silently, doing nothing.
- **`pgrep -f X` has the identical hazard**, and `for p in $(pgrep -f X); do kill
  $p; done` is the same self-kill one step removed.

So: never match a process by a pattern that appears anywhere in the command doing
the matching. To *look*, use
`ps -eo pid,etimes,args | awk '/name/ && !/awk/'`, which cannot kill anything.
To write a script whose name resembles a running process, write it with `python3`
rather than a heredoc, and check `wc -c` before launching -- a truncated launcher
exits 0 and leaves no output.

## `php2phpt` over the whole tree collapses after ~520 tests

One process over all of `Zend/tests` stops producing output partway through.
Every test after that point scores `errored` with `--- NO TEST ---`, and the
summary reads

    4966 tests: 439 passed, 11 failed, 29 unsupported, 0 compile-error, 4447 errored, 40 skipped

which looks like the interpreter has been destroyed. It has not. The tell is
that **`passed` stops moving**: `-n 500` gives 422 passed / 2 errored, `-n 560`
gives 439 / 41, `-n 600` gives 439 / 81, and the full tree gives 439 / 4447 --
the same 439 every time, with `errored` growing one-for-one with the tests
beyond the cutoff. Measured 2026-09-23.

Individually the same tests are fine: `Zend/tests/traits` alone is 161/165
(98.8%), `Zend/tests/generators` 134/159 (85.4%), and the first 520 are 84.4% of
attempted -- which is the recorded baseline. So **give each chunk a fresh
process**; `.agents-workspace/tmp/interp/run.sh` does one chunk per
subdirectory plus the top-level files split eight ways.

**And run the chunks sequentially.** `-P 4` looks like it works -- every chunk
reports a summary and the test count sums correctly -- but it invents
`compile-error`s: 433 of them across the run, **all** in the file-list chunks
and none in the per-directory chunks. Re-running one of those chunks alone gave
`0 compile-error` and 84.2%, against the 80 the parallel run had attributed to
it. `php2phpt` takes its own `os.MkdirTemp` per process, so the contention is in
the linked engine rather than the scratch directory; either way the corpus is
`-P 1`. The compiler corpus is fine at `-P 4` and is not evidence about this
one.

A bucket that appears *only* under parallelism is the tell. Compare a suspicious
chunk against itself run alone before believing any total.

Two traps while building that runner, both cheap to repeat:

- `xargs -P4 -I{}` substitution does **not** survive a work item the child shell
  has to split again. `set -- {}` where `{}` is `d /path` ran `cat f` and wrote
  two empty outputs. Encode one token with no spaces (`d:/path`) and split it
  with `${item%%:*}` inside the child.
- A chunked run whose items are wrong still prints `ALL-DONE` and leaves output
  files behind. Count the outputs against the number of chunks before summing --
  62 expected, 2 present.

## Editing `runtime/` mid-run: which harness cares

`php2gotest` **compiles generated Go against the live tree** for every file, so
editing `runtime/php` mid-run breaks every file not yet reached (see below).

`php2phpt` does not. It is a single binary that interprets, so once the runner
has built it the tree can be edited freely without touching the run. The
distinction is the per-test `go build`, not the corpus.

## Do not edit `runtime/` while a corpus run is in flight

A corpus run builds each generated program **against the live tree**. Editing
`internal/gogen` mid-run is safe -- that is compiled into the harness binary the
run already has -- but editing `runtime/php` or `runtime/stdlib` is not: a
generated program links them at test time, so a half-written file makes every
file still to be graded fail to build.

Measured, on run `zi2` (2026-09-19): a `runtime/php/coerce.go` that did not
compile for a few minutes cost **248 files**, and the run reported

    passed 2238  failed 987  unsupported 432

against a real baseline of 2383. It looks exactly like a catastrophic
regression -- 147 files "lost", alphabetically contiguous.

Use `.agents-workspace/tmp/seg/tally.sh <run> [baseline]`, which sums **every**
bucket and refuses to print a comparison when the run is not a result.

What it checks, and why each matters:

- **The buckets must add up** to the file count. On `zi2` they summed to 3,658
  against `files 4986`.
- **No file may fail with `the runtime failed to build`.** That reason, and only
  that one, means the tree was edited mid-run. The harness classifies it
  correctly on purpose -- see the comment at the `go build` step in
  `cmd/php2gotest/main.go`, written after the same thing happened to a
  half-written `iter.go`.

**`noreference` is not normally zero.** It sits around 1,078 -- tests the engine
itself bails on, plus `.phpt` skips -- and that is part of the 4,986 denominator
every number in the journal uses. Refusing a run for a non-zero `noreference`
would refuse every run; the reason is what distinguishes them.

So: rebuild the tree (`go build ./...`) before launching, leave `runtime/` alone
until the run finishes, and tally with the script rather than by hand.

## Measuring compiled output's speed: `hack/bench.sh`

```sh
eval "$(hack/cgoenv.sh)" && hack/bench.sh [-n RUNS] [-o DIR] [-p START:DUR] [-E] [file.php...]
```

With no files it times `hack/bench/*.php` and the engine's own
`Zend/bench.php` and `Zend/micro_bench.php` (with their
`date_default_timezone_set` block removed, which compiled output refuses),
compiled against `php2eval -raw`, and prints the median wall clock of each arm
and the ratio. Programs' own output -- bench.php prints per-test timings -- is
kept under `DIR/out`; `-p` writes a CPU profile of the first compiled run to
`DIR/prof`.

- **It measures the `gogen` path.** `php2gotest -emit DIR` writes each
  program as a module and stops. `php2go bundle` embeds the interpreter, and a
  profile of a bundle is a profile of `interp.execFrame`.
- **The emitted modules build against the live `runtime/`**, through the
  `replace` in their `go.mod`. Editing the runtime while a benchmark run is in
  flight changes the later programs; the same rule as a corpus run.
- **Nothing else heavy at the same time.** A fixture or corpus run alongside
  moves the numbers; chain them instead.
- **A profile needs a window that closes before the program exits**
  (`-p 0:5`): a compiled program leaves through paths that never return to
  stop the profiler, and an unstopped profile is not written. `perf` is not
  permitted on this box.
- **Wall clock includes start-up on both sides**, ~20 ms for `php2eval`, so a
  ratio on a sub-second program understates the gap.

## Default Go suite

```sh
go test ./...
```

Use focused package tests while iterating, then run the full suite before
completion. Bazel targets exist alongside; keep `BUILD.bazel` files in step when
adding Go files or testdata.

## The differential oracle

`runtime/interp/differential_test.go` is the primary correctness surface. Every
case runs twice — once through the linked engine, once through the interpreter —
and must match exactly. Add a case for every behavioural change; hand-written
expected output only tests what the author believed PHP does.

Two states must be equal for the comparison to mean anything:

- **State.** Anything the engine keeps across an eval but the interpreter gets
  fresh must be cleared in `Engine.Reset` — user constants, `EG(symbol_table)`,
  `EG(error_reporting)`, the SPL autoload registry. The symptom of a leak is a
  case that passes alone and fails in the suite, or the reverse.
- **Capabilities.** Both sides need the same ones: install `ir.EngineLoader` and
  pass the harvested builtins into the manifest, or working PHP is compared
  against a missing-compiler or undefined-function error.

`cmd/php2eval` is the interactive form of the same thing:

```sh
php2eval -c '<php>'          # engine and interpreter, side by side
php2eval -c '<php>' -engine  # the engine alone
```

Settle behavioural questions here rather than by reading `zend_vm_def.h`.

`runtime/php/engine_parity_test.go` covers the value model against the engine,
and `cmd/php2phpt/lifetime_test.go` covers request-shutdown behaviour that the
differential oracle cannot observe — the cases live in `testdata/lifetime`.

## Grading against a corpus

```sh
php2phpt <dir>              # summary plus the top blocking opcodes
php2phpt -diff 20 <dir>     # expected vs actual for the first 20 mismatches
php2phpt -v <dir>           # list failing tests
```

Outcomes are `passed`, `failed`, `unsupported`, and `errored`, and the split is
the point: `unsupported` is an honest interpreter gap, `failed` means it ran and
diverged, `errored` means a Go error escaped the harness. `failed` rising while
`unsupported` falls is the expected shape of progress and the two numbers only
make sense read together.

Two rules that fall out of the load sensitivity below, and out of a two-session
handoff in which five claims were corrected while their conclusions survived:

- **Every number in these documents names the command that regenerates it.**
  A figure with no way back to its source cannot be re-checked, and in a handoff
  it is the thing that goes stale silently — a correction landing in one document
  leaves the same claim standing in another, and nobody re-reads a whole file
  each round.
- **Never cite a position.** `TODO.md:979` was correct when written and wrong
  six lines later, because an edit *above* it moved the section — made in the
  same round as the rule above, which is how cheaply this happens. Cite a section
  name, a symbol or a command; those survive edits elsewhere in the file.
- **Quote what survives the noise.** On this box a benchmark ratio of two orders
  of magnitude is solid while the nanosecond counts under it move several percent
  between runs. Give the ratio; give the absolute figures only with the load they
  were taken at, and re-measure on a quiet machine before deciding anything with
  them.
- **Prefer counts to durations — but only counts of a deterministic property.**
  Allocations per operation, slots in a frame, bodies lowered: these do not move
  under load, which is why a decision made on a busy machine can rest on them.
  The struct-versus-slice representation choice rested on 1 allocation against 2
  and needed no caveat at all, while the timing beside it needed three.
  The exception is sharp and this session produced it: `compile-error` **is** a
  count and it moved between 0 and 71, because it counts *outcomes of an
  operation that can fail under resource pressure* rather than a property of the
  artifact. Ask which kind of count it is before trusting it.

`compile-error` is **load-sensitive on a shared machine**, and it silently moves
`passed`. Identical runs of one binary returned anywhere from 0 to 71
`compile-error` as the box's load average moved around 25 (2026-08-28).
`attempted` excludes that bucket, so `passed` follows it -- 4140 to 4200 across
those runs -- while `pass rate` held at 89.1% throughout. Quote the rate,
`failed` and `unsupported`; **`passed` alone is not safe to quote from a loaded
machine**, and a single before/after pair of runs can show a swing of 60 tests
in either direction that is entirely noise. Check `uptime` before believing a
count moved, and repeat the pair alternately rather than back to back.

The productive loop is: dump the diffs, cluster them by which diagnostic is
missing or spurious, fix the largest cluster, re-measure. Put the clustering
script and its dumps in `.agents-workspace/tmp/`.

Before attributing a `.phpt` failure to the runtime, check what `run-tests.php`
actually does — the harness has been the wrong party before.

## Rebuild `php2eval` before every probe

A probe binary built with `go build -o "$CLAUDE_JOB_DIR/tmp/php2eval" ./cmd/php2eval`
is **not** refreshed by `go build ./...`. After changing the runtime, rebuild it
explicitly or the probe reports the previous build's behaviour.

This is easy to lose half an hour to, because the corpus runner *is* current —
`go run ./cmd/php2phpt` compiles each time — so the symptom is "the corpus moved
but the probe did not", which reads like a bug in the change rather than a stale
binary.

## Do not touch the tree while a compiler corpus run is in flight

`php2gotest` emits Go and then runs `go build` on it, and the generated program
imports `github.com/moriyoshi/php2go/runtime/php` **from the working tree**. The
compiler binary is frozen at the moment it was built; the runtime it compiles
against is not. Change a signature in `runtime/php` mid-run and every program
emitted after that point fails to build, while the binary keeps emitting calls
in the old shape.

The symptom is a catastrophic, entirely fake regression. One run gave:

	passed         228  (was 717)
	errored        859  (was 0)

with all 859 the identical `go build: too many return values`. **The tell is
that the corpus is walked in filename order**: zero errors before `d`, then
every case erroring from the point the run had reached when the edit landed. A
real regression does not respect alphabetical order, so check that before
believing the number.

This is the mirror of the stale-probe trap above -- there the binary was old and
the tree current; here the binary is old and the tree has moved *under* it.
Either start the run and leave the tree alone until it finishes, or copy the
tree to `.agents-workspace/tmp/` and run there.

## The two fixture sets are not one, and each needs the other

`internal/lower/testdata` and `cmd/php2gotest/testdata` look like they should be
merged and should not be. They answer different questions under opposite
pressures:

|  | `internal/lower/testdata` | `cmd/php2gotest/testdata` |
|---|---|---|
| graded by | a checked-in `.ir` golden | stdout against the engine |
| wants to be | small, stable, **output-free** | broad, growing, **printing** |
| may contain | constructs the emitter refuses | only what compiles and runs |

A differential case that prints nothing tests nothing, while `echo` in an IR
fixture buries the construct the golden exists to show. Growing a differential
case -- the normal way they improve -- would churn a golden every time. And
`fiber.php`, `resumable.php` and `catches.php` exist precisely to pin the
lowering of things the emitter refuses; in a shared corpus they would be
`unsupported`, or would get trimmed to suit the emitter and lose what they cover.

**What they do owe each other: a construct whose IR shape is pinned must also be
executed somewhere.** A golden checks shape, never behaviour, and twice that gap
hid a defect:

- `sprop.set` was pinned in `write.ir` and no program ran it. It compiled,
  passed, and was never exercised.
- `foreach.php`'s `total()` bound a `$k` it never **used**, so a dead `iter.key`
  looked correct -- and mem2reg's dropped key binding survived behind it,
  emptying the key of every keyed `foreach` in a function.

So when adding an IR golden for a new construct, add the differential case that
runs it, and make the case *use* what the construct produces. `joined()` in
`foreach.php` and `containers()` in `specialized.php` are the pattern: the
fixture exists in both sets, shaped for each.

## A hang is graded, not waited on

`php2gotest` bounds every child process -- the compiled program and the
`php2eval` oracle -- at `-timeout` (30s by default) and grades an overrun as
**`errored`**, the bucket that is meant to stay empty. Not `failed`: there is no
output to compare, and a program that does not terminate is not a wrong answer.
Not `unsupported`: it is not a gap the compiler declared.

This exists because a hang was the one failure mode the harness could not
report. One looping generated program left a whole run wedged with no summary at
all -- an hour of wall clock, still in the `b`s, with a `prog` spinning on a
`throw` routed into its own `finally`. The tell was `pgrep` showing a
`/tmp/php2gotest-*/prog` still alive; there was nothing in the output to see.

If a legitimate case needs longer, raise `-timeout` for that run rather than
removing the bound, and say so beside the number: a run with a different timeout
is a different measurement.

## A measurement with compile errors is noise

The machine running the corpus is shared. Under load the engine's compiles fail,
and `php2phpt` reports that honestly -- but the failures land in the
`compile-error` bucket and drag `passed` down with them. Five consecutive runs
of one binary have given 4200, 4195, 4181, 4145 and 4191 passed, with
`compile-error` 0, 7, 23, 60 and 12 respectively.

**Read the compile-error count first. If it is not 0, re-take the measurement.**
A before/after pair is only comparable when both runs are clean, and a
diff-set computed from a noisy run invents fixes and regressions that are only
tests that failed to compile.

## A runtime failure with nowhere to go is a masquerade

An error the interpreter returns has to reach the classifier. Any place that
*handles* some errors and drops the rest will turn a gap into wrong output --
and sometimes into a pass, if the visible part happens to match.

`runShutdown` was such a place: it acted on `exit()` and on a thrown exception
and discarded everything else, which is exactly the set of this runtime's own
gaps. Three tests were misclassified by it, including one that counted as
**passed** while php2go silently skipped an allocation it cannot model.

When adding a driver loop that calls into the interpreter, decide what happens
to a non-PHP error before writing the PHP cases.

## The outcome counts are load-bearing, not just the failing set

A round's diff-set compares the *names of failing tests* before and after. That
misses a test moving between any two other buckets -- and `failed -> errored` or
`passed -> errored` is exactly what a half-right change produces. Round 306
turned one passing test into an interpreter error while the failing set looked
clean; the only sign was `errored` going 2 -> 3 in the summary line.

Read all five numbers each round: `passed`, `failed`, `unsupported`,
`compile-error`, `errored`. When one moves in a direction the change does not
explain, find the test before writing the round up.

`php2phpt -diff-kind errored -diff N` lists them.

## Choosing what to fix: rank by missing output, not by diff size

A failing test whose output *stops* partway usually has one bug near the point
it stops, and everything after is downstream noise. A test that differs on one
line out of three may need a mechanism php2go does not have.

So rank the failing set by `len(expected) - len(matching prefix)`:

```python
pre = 0
for x, y in zip(expected_lines, got_lines):
    if x != y: break
    pre += 1
rows.append((len(expected) - pre, name))
```

Rounds 308 and 309 both came off the top of that list: `operator_unsupported_types`
(1983 expected, 1023 produced, three bugs behind one `die()`), `weakrefs_006`
(91 -> 31), `bug52193` (93 -> 17). Reading the *first divergence* and walking
forward is faster than reading the diff as a whole -- a unified diff of a
truncated output aligns badly and reads as hundreds of unrelated changes.

## The census is a second implementation of the emitter, and it drifts

`php2gotest -census` walks every body through `gogen.Gaps`, which is **not** the
code path that emits. On 2026-09-09 it reported `$this outside a method` in 162
files, second only to the library surface -- and every one of those files
compiles. `Gaps` set no receiver, so it refused what the emitter accepts. It
also walked only the file body, plain functions and instance methods, so gaps in
**static methods, property hooks and closures counted zero**.

Before acting on a census entry, check one file against its grade. A file
reported as having a gap that is nonetheless `failed` rather than `unsupported`
is the tell: it compiled, so the gap is the census's and not the compiler's.

## `php2gotest -v` prints its summary twice, and never names a passing file

Two shapes of the same trap, both hit on 2026-09-15 while tallying a 14-chunk
run:

- The summary block is printed **twice** -- once over every file, once with the
  skipped (`noreference`) ones removed. Summing every `^  passed` line across
  chunk outputs therefore doubles every bucket, and the doubled numbers look
  plausible because their ratios are right. Take the four lines that follow the
  `^files N` header and no others:

      for f in out-*.txt; do grep -aA4 '^files ' $f |
        grep -aE '^  (passed|failed|unsupported|errored)'; done |
        awk '{a[$1]+=$2} END {for (k in a) print k, a[k]}'

- `-v` lists only the files that did **not** pass. There is no `passed <path>`
  line to grep for, so the passing set is the corpus list minus every file
  listed with a status prefix:

      grep -ahE '^(failed|unsupported|errored|noreference) +' out-*.txt |
        awk '{print $2}' | sort -u > nonpass.txt
      comm -23 all.txt nonpass.txt > pass.txt

  Cross-check the result against the run's own `passed` count before diffing it
  against anything. A set built by the wrong pattern comes out empty or complete
  rather than wrong-by-a-few, which is the one failure mode a diff will not
  show you.

Keep the per-file passing set of every measured revision, not just its counts.
Reconstructing which files moved needs the previous set; without it a net figure
is all you have, and a net figure hides compensating movement (see below).

## The census ranks compile-time refusals, and a run-time gap hides behind one

`-census` reads what the **emitter** refuses. A gap the generated program
declares at *run* time -- `php.Unsupported` reached while the program executes
-- is invisible to it, because the census never runs anything.

So a "singleton" count is the number of files with one compile-time gap, not the
number a fix will move. On 2026-09-15 the census listed eight files whose only
gap was `library function json_encode`; implementing it moved **none** of them,
because six stopped one step later on a property-hook refusal raised from inside
the emitted code and two on `include or eval`.

The exposure is systematic rather than bad luck: a library function whose first
use is on an *object* reaches the class table, and the class table is where the
compiled back-end's remaining run-time refusals live. Before costing a library
item off a singleton count, read what the candidate files actually do with it --
`grep -l` for the object-shaped uses is enough -- or accept that the count is an
upper bound with no lower one.

### A partial run's last chunk is mid-write, and its unreached files look passing

Checking a long run early is worth doing -- a change that starts *rejecting*
values wants a regression signal before the full 75 minutes are up. But the
passing set is built by subtracting the files a chunk **listed** as non-passing,
and a chunk still being written has not listed the ones it has not reached. They
come out as gains.

On 2026-09-16 that read as thirty-odd `closure_*` files newly passing from a
change to argument type verification, which was the in-progress chunk and not a
result at all. Filter to the chunks that finished:

    for f in out-*.txt; do grep -aq '^files ' $f && echo $f; done

`^files N` is the summary header, written only at the end. Comparing the same
subset of chunks on both sides then gives a true partial diff -- the four
complete chunks showed one real gain and one flaky file, which is what the full
run confirmed.

### A backtick in a differential case terminates the case

The cases are Go **raw** string literals, so a backtick inside one ends it --
and the parse errors land dozens of lines away from the case that caused them.
On 2026-09-16 a comment mentioning an attribute in backticks reported
`illegal character U+0023` at a line 10,000 earlier than the edit.

Backslashes are safe inside a raw literal and need no doubling, which is why
`#[\SensitiveParameter]` in a case is written with one backslash. Only the
backtick is fatal, and a prose comment is the usual way one sneaks in.

### A stale `php2eval` disagrees with the in-process interpreter

`go test ./runtime/interp` runs the interpreter **in process** and is always
current. `php2eval` is a separately built binary and is not, so a probe through
it after changing `runtime/*` compares the engine against an interpreter from
some earlier build.

On 2026-09-16 that cost twenty minutes chasing a `unserialize()` difference that
had already been fixed: the stale binary still ran the interpreter's own copy of
the function, which had just been moved to `runtime/stdlib`. The giveaway was a
behaviour *unique to the new code* -- `unserialize("")` answering silently --
still showing the old warning.

So: rebuild it in the same command that probes with it, and when the two
disagree about a change you just made, suspect the binary before the code.

    go build -o $S/php2eval ./cmd/php2eval && ... | $S/php2eval

### A path that does not exist is dropped, not reported

`php2gotest` counts the files it graded. A path that names nothing is **silently
omitted** -- there is no "no such file" line -- so a command given four paths and
one typo reports three files, and the pass total looks like it covers all four.

On 2026-09-16 that produced a wrong claim in a journal entry:
`serialize_unserialize_error.phpt` was reported as passing when the real file is
under `generators/errors/`, the typo'd path was dropped, and the remaining
fixtures' passes were read as covering it. The next measurement showed no gain
and the entry had to be corrected.

So when running a named set, check the count:

    $S/php2gotest ... $LIST | grep '^files '   # must equal `wc -l < $LIST`

The full-corpus runs are safe -- their lists come from `find` -- but any
hand-typed path deserves the check.

## Compute the pass rate, do not carry it forward

The counts in a journal entry come from the tally; the percentage beside them
does not, and on 2026-09-09 eleven entries carried one computed in my head. The
error grew as the numbers moved -- 969 / 373 was written up as 74.3% against a
true 72.2% -- because each entry nudged the previous figure rather than dividing.

The rate this project quotes is **passed / (passed + failed)**: the share of what
php2go actually compiles that it gets right. `unsupported` is deliberately not in
the denominator, which is why the figure can rise while coverage does not move.

    awk '{printf "%.1f%%\n", 100*$1/($1+$2)}'   # passed failed

A percentage is a claim, and QUALITY_GATE.md's rule about quoting before-and-after
from the same command covers the derived figure too.

## `want ""` is a missing diagnostic, and it is the biggest cluster

PHP prints a diagnostic as `\nWarning: ... in FILE on line N\n` -- with a
**leading blank line**. So when the harness reports

    line 12: want "" got "int(1)"

the expected line 12 is that blank and line 13 is the diagnostic: we omitted a
warning, notice, deprecation or fatal that PHP emits. It reads like a formatting
difference and is not one.

That made `want ""` the largest cluster in the 393 compiler failures -- 37 of
them -- and grouping *within* it, by the expected line that follows the blank,
turned it into a work list: 17 missing fatals, several missing notices, one
missing bitwise-not TypeError. One such group was six tests all missing method
visibility enforcement.

Recovering the following line from the `.phpt` needs care: `--EXPECTF--` does
**not** carry the leading blank when the diagnostic is the file's first output,
so the offset between the reported line and the section's own lines is 0 in some
files and 1 in others. Print both candidates rather than picking one.

## Rank by *real* differing lines, ignoring wildcards

The missing-output ranking finds tests that stop early. A second ranking finds
a different kind: count only the differing lines that carry **no `%`**, since
the EXPECTF matcher covers those anyway.

Tests that come out at *zero* real differences are failing for a reason the
line-by-line diff cannot show -- almost always two whole blocks printed in the
wrong order. `bug52361` (round 314) was one: both stack traces were correct and
the two halves were swapped.

## A differential case can depend on the engine process, not just on its own code

The differential harness resets the engine between cases, but not everything is
in the reset. The GC **root buffer** is not: a case ending in
`var_dump(gc_collect_cycles())` counts what the cases *ahead of it* left behind
as well as its own cycle.

That makes such a case's expected value a function of the whole file above it.
Round 323 deleted one line from one case and a *different* case started failing,
`int(2)` becoming `int(0)`. Nothing in the interpreter had changed.

Two consequences:

- Open any case that counts collections with a bare `gc_collect_cycles();` so it
  measures only itself.
- When a differential case fails after an edit that could not have touched it,
  suspect process state before suspecting a regression. The other direction --
  a case that passes only because of what ran before it -- is worse, and is why
  the drain belongs in the case rather than in the harness.

### Restoring to a convenient value converts a forward coupling into a backward one

The `fiber.stack_size` case, found by `GATE_REPEAT_TESTS=1` and not by anyone
noticing: it passed at `-count=1` and failed at `-count=2` on a clean tree.

It already carried a comment about the coupling, and a fix for it:

```php
// The engine's ini table outlives one case, so the setting is put back:
// a later case that starts a fiber would otherwise find no stack for it.
ini_set("fiber.stack_size", "1M");
```

**That restore is what made the case inherit from its own previous run.**
`ini_set` answers the *previous* value, the case prints it, and after one run the
previous value is the `1M` the restore left rather than the empty it starts life
with.

So the forward fix created the backward bug, and it looks like cleanup, which is
why it survived. The rule:

> **Restore what you found, or seed what you read.** Restoring to a third value
> chosen to suit whatever runs next does not remove a coupling -- it turns a
> forward one into a backward one.

The case now seeds `ini_set("fiber.stack_size", "")` before the loop, so the
value it *reads* is set by the case, and keeps the `1M` restore for the forward
direction, with each comment saying which direction it serves. Verified at
`-count=3` rather than `-count=2`: two runs only show that the second equals the
first, which a coincidence also satisfies.

One case out of 1,077 -- worth stating as a measurement rather than an absence.

## Eliminating yourself from a set of two does not identify the other member

The fifth instance of the two-populations error this session, and **the first in
a person rather than a tool.**

A `GATE RED` appeared -- `include.go: undefined: FileResolver`, plus an
unformatted `runtime/bundle/bundle.go`. Evidence was gathered:

| | what it establishes |
| --- | --- |
| `go build ./runtime/php/` is clean | **not mine** |
| `git diff include.go` shows the use as an *added* line | recent — not whose |
| `runtime/bundle/` is untracked and new | new — not whose |
| `bundle.go` is the gofmt half of the same RED | same author as itself — not who |

None of it identifies an author. Every row is equally consistent with a third
writer. What was reported was "the current RED is entirely theirs", and the step
from *not mine* to *yours* was an assumption never stated and never checked:
**that the candidate set had two members.** It has at least three -- the other
session had done no work under `runtime/` at all.

> Eliminating yourself from a set of two does not identify the other member
> unless you have checked that the set has two members.

The other four instances -- `unsupported`, `UNSTABLE`, `FLAKY`, `errored` -- were
buckets in software. This one was a bucket in a head, with no code to blame,
which suggests the tools inherit the error from whoever specifies them and the
specifying step is where the missing case gets dropped.

Two practical consequences:

- **On this tree, "not mine" means only that.** Neither session may convert it
  into an attribution.
- **Interleaved mtimes beat a diff for authorship.** A diff shows that something
  changed; timestamps interleaved with your own edits distinguish *concurrent*
  from *sequential* work. `runtime/bundle/bundle.go` at 01:36:47 sat between the
  other session's own edits, which is what settled it.

A note on how the same question was answered correctly earlier the same day:
asked to accept authorship of `finally.ir` and `foreach.ir`, refusing was easy —
*knowing* nothing had touched `internal/lower`. Here there was no knowledge of
who had touched `runtime/bundle`, and the gap was filled with an inference
instead of left empty. **Right with knowledge, wrong with elimination.**

## Assert the rendered form, not the fields that compose it

A test for `InitThrowable` asserted the property **names and their order**. It
passed while the order was wrong, the visibility annotations were missing
entirely, and the keys were unmangled -- three defects, none of them among the
things the test had thought to check.

Replaced with one assertion: render through `VarDump` and compare with the
engine's `var_dump` output byte for byte. That pins order, visibility and
mangling together, and would have caught all three on the day the code was
written.

> A test written from the parts you thought of checks the parts you thought of.
> The rendered form checks the ones you did not.

Applies wherever output is graded byte-exact downstream, which here is
everywhere: prefer asserting the text a consumer will compare over the fields
that produce it. The fields are an implementation detail; the text is the
contract.

## A mutation check must prove the mutation applied

Mutation-checking the throwable hierarchy meant rewriting `ParseError` to extend
`Error` -- the plausible-from-memory answer -- and running the test. It reported
**ok**, and the conclusion on offer was *the test is weak*.

The substitution had silently not matched. `gofmt` had aligned the map and the
pattern guessed the spacing, so nothing changed and a passing test was read as a
failure to detect.

Redone with proof: `assert s2 != s`, the file's checksum before and after
(`695a9471` -> `4f02b94e`), and a `grep` showing the new line. It then failed
with exactly the right message.

> A mutation check that cannot show the mutation applied is not a check. A no-op
> reads as a weak test, and the repair for a weak test is to weaken a good one.

This is the guard-that-cannot-distinguish-its-own-failure family, one level in:
the *check on the check* had the same defect the check was there to find.
Practically: mutate by assertion (`assert old in s`), not by best-effort
substitution, and print the checksum.

## When reading a value back from the engine, vary the value

Third instance in one day, and stable enough to be a rule rather than an
observation:

| table | what it asserted | what it missed |
| --- | --- | --- |
| `InitThrowable` | property names and order | visibility, mangling, a missing property |
| `ArrayKey` | the rows thought of | `NAN`, the infinities, int64 overflow |
| the diagnostics table | 19 conditions, one benign name each | any name containing `\`, `"` or `%` |

Every one was read back from the engine, which is right. Every one was read back
with **one input per condition**, which is one axis exercised and one assumed.

The diagnostics case is the clearest. `Class %q not found` is correct for every
name it was probed with and wrong for every **namespaced** one, because Go's `%q`
escapes the separator. The probe and the bug lived in different *inputs*, not in
different code.

> Vary the value, not only the condition. A table with N conditions and one input
> each has tested N points, not N behaviours.

And the value to vary by is not exotic: a namespaced class name **always**
contains a separator, so this was the common case rather than an edge one.

### `%q` is not PHP quoting

Where a PHP message quotes a name, the quotes are part of the message and the
bytes go in raw. PHP escapes **nothing** -- not the separator, and not an
embedded quote:

```
Class "Foo\Bar" not found
Undefined array key "say "hi""
```

Go's `%q` escapes backslashes, escapes quotes, and would escape non-printables.

The repair is *not* to concatenate the quotes around the name -- that moves the
bug one step over, since a name containing `%` then becomes a format verb, which
`go vet` reports as a non-constant format string. Keep the quotes **literal in
the format** and pass the name as `%s`.

## For a diagnostic, the kind is a bigger risk than the text

An audit of hand-written PHP-visible strings found **three of four wrong in kind
rather than in wording**: conditions the engine *throws*, emitted as Go errors.
The words were right. A PHP `catch` cannot see a Go error, so each was a
catchable condition made uncatchable -- the program takes a different path, and
no amount of comparing message text finds it.

So when settling a diagnostic against the engine, read back **both**:

```php
try { $f(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(); }
```

`get_class` first. The class is not guessable from the message --
`Unsupported operand types` is a `TypeError` among a list of plain `Error`s, and
`Division by zero` and `Modulo by zero` are two messages sharing
`DivisionByZeroError`.

And the mechanism can differ between conditions that look like one another:
calling a method on an int **throws**, reading a property on the same int
**warns** and yields NULL.

The defence is structural rather than careful reading. In `runtime/php`, a
`Throw*` returns an error carrying a class and a `Warn*` writes through the Ctx
and returns nothing, so neither can be used in the other's place: the warning has
nowhere to put a class, the throw has nowhere to put a Ctx.

## Go pointer semantics leaking through PHP value semantics

Two instances, one on each side, found the same way and never by review:

| | the bug | what exposed it |
| --- | --- | --- |
| `yield from` over an array | the delegate read values **live** through a shared `*Array` | a write to the array mid-delegation |
| `$c = $b` | assignment **shared** the `*Array`, so `$c[0] = 99` gave `99\|99` | a write through the copy |

PHP arrays are value types and Go slices/maps behind a pointer are not, so every
place a `*Array` crosses a boundary is a candidate. **Both stayed invisible until
something wrote**, because reading through a shared pointer and reading through a
copy are indistinguishable.

So the test for this class is not review, it is: *find every place a `*Array` is
handed on, and write through the far end.*

The rule the other side landed on -- clone an array, share an object -- is PHP's
own, and it has to be gated on the slot being a `zval`: a `foreach` iterator is
a `*php.Iter` and must never be cloned.

## A net movement of one is not "noise", it is an unread measurement

A tick on 2026-09-15 measured 2056 passing against a 2057 baseline. A one-file
drop is small enough to read as run-to-run variation, and it was not: the diff
against the previous passing set showed **two regressions and one gain**, and
the regressions were a real bug in the change under test.

The count is a sum, so every net figure is compatible with arbitrarily much
movement underneath it. `failed` rising while `unsupported` falls is already
called out in QUALITY_GATE.md as needing both numbers read together; the same
holds one level down, between files inside a single bucket. **The unit of
measurement is the passing set, not the passing count.**

When the set does show a regression, identify it by measurement rather than by
reading diffs and judging which looks new: build a second binary with the change
reverted, run the candidate files against both, and compare. On the same tick,
six of the eight files whose diff mentioned a stray `&` were already failing for
unrelated reasons, and picking them out by eye would have sent the fix at the
wrong target.

## An unchanged number says you broke nothing, not that you fixed something

The `spl_autoload_register` move changed behaviour deliberately: a duplicate
registration no longer adds a second entry, because the engine does not add one.
The acceptance run came back **identical**, 130 tests of reach.

That is worth reading carefully. The reach bound says what the run could
**detect** -- 130 tests could have caught a regression. It says nothing about
what the run could **confirm**: no corpus test registers the same autoloader
twice, so the fix is invisible to it.

So two different claims come out of one green run, and only the first is
supported:

| claim | supported by |
| --- | --- |
| the move broke nothing | the acceptance run, 130 tests of reach |
| the behaviour change is correct | the engine probe and the conformance test |

Reporting a single number invites conflating them. When a move deliberately
changes behaviour, say which evidence carries which claim -- and if the corpus
cannot see the change, say that too, rather than letting the green stand for
more than it covers.

## A filter over syntax is not a filter over dependencies

Selecting builtins that could move from the interpreter meant finding the ones
that touch nothing interpreter-local. The filter scanned for **call syntax** --
`name(` -- and pronounced eighteen clean.

Three were not:

| | what the filter missed |
| --- | --- |
| `array_unique` | interpreter sort-flag **constants**, used as bare identifiers |
| `strcoll` | the `libc` **package**, and the `UnsupportedError` **type** |

Types, constants and package qualifiers are dependencies that never appear as a
call. The compiler caught all three immediately -- `undefined:` on the first
build -- so the cost was minutes, but **the filter had said "clean" about
functions that were not**, and anything that happened to compile would have gone
through.

> The compiler is the only thing that actually knows what a piece of code
> depends on. A hand-rolled scan is a hypothesis; build it to find out.

Practically: when a script decides what is safe to move, treat its answer as a
*candidate list* and let the build adjudicate, rather than as a result. The same
shape as the dangling-label guard on the emitter side -- structural enforcement
over a predicted set.

## A moved builtin goes back on the path it was on

`printf` and its family moved from `runtime/interp` into `runtime/php`. The
moved code was **byte-identical to the original** -- diffed against the staged
version, the `%s` case character for character the same -- and it broke:

```
printf("[%s]", $objectWithToString)   =>  []       (want [STR])
sprintf(...) on the same object       =>  [STR]    (fine)
```

What changed was **where it was dispatched**. The original was reached through
the interpreter's `builtins` table, and immediately before that call sits
`verifyBuiltinArgs`, which applies the engine's **arginfo**: an object handed to
a `string` parameter is converted through `__toString` on the way in. The move
routed it earlier in the pipeline, past that step.

The function never saw the difference. The pipeline around it did.

> Where a function is dispatched is part of its behaviour, because the pipeline
> around it does work the function itself never sees. **A byte-identical body is
> not evidence that a move preserved behaviour.**

That is the sharp edge of *moved, not copied*: "same code, one implementation"
is necessary and not sufficient. When relocating, identify what runs *around*
the old call site -- coercion, verification, materialisation, output buffering --
and put the function back on that path rather than on a convenient one.

Fixed by bridging rather than re-routing: the interpreter registers
`php.BuiltinNames()` into its own table at init, so every shared function keeps
its original dispatch. The two that were dispatched elsewhere are deliberately
skipped by the bridge, for the same reason.

## "The number must not move" is unusable on a shared tree

An acceptance criterion of the form *the corpus must be exactly N, revert if it
moves at all* cannot be applied where more than one session is writing, because
**someone else moving it looks identical to you moving it.**

It happened: after `php.CallValue` landed, the interpreted corpus read
4,363 / 195 / 151 / 4 against a baseline of 4,362 / 195 / 154 / 2 -- four tests
changed bucket. Applying the rule literally would have reverted correct work.

The usable form is *the number must not move **because of me***, and that needs a
**reachability bound**, which is checkable:

| | |
| --- | --- |
| corpus files using `array_reduce` | **1** (`bug67856.phpt`) |
| so the change can move at most | 1 test |
| tests that actually moved | 4 |
| `bug67856.phpt` run on its own | passes |

Arithmetically outside what the change can reach, so it is not the cause.

This is the dependency-path rule for a RED gate, one level over -- from *can the
break reach my measurement* to *can my change reach the number*. Both replace a
policy with a measurement, and both are cheap: one `grep -rl` over the corpus
bounds the reach before any argument about causes.

**And the bound is worth computing before the run, not after.** Knowing a change
touches one test tells you what the acceptance criterion can possibly detect --
which for a one-test reach is almost nothing, so the conformance tests carry the
weight instead. Contrast the `set_error_handler` move at **135 tests of reach**:
there, an unchanged corpus is evidence.

### The baseline is the run before your change, not a number you remember

Three consecutive runs on this tree:

```
4362 / 195 / 154 / 2      <- historical
4363 / 195 / 151 / 4      <- after someone else's work landed
4363 / 195 / 151 / 4      <- after mine
```

Against the **oldest** the last change "moved the corpus" and causality has to be
re-litigated. Against the run **immediately before it**, on the same tree, it is
exactly unchanged.

On a tree several sessions are writing, a remembered baseline silently
accumulates everyone else's work into your diff. Take the reading immediately
before you start, and compare to that.

### The three rules together

Each replaces a judgement with a measurement, and between them they cover the
three ways this went wrong in one day:

| | failure it prevents |
| --- | --- |
| bound the reach **before** the run | a criterion that can detect nothing, trusted as if it could |
| compare against the run **immediately before** | a movement that was never yours, attributed to you |
| **enumerate your symbols** if it still moves | arguing about a cause you cannot determine |

### Enumerate the paths rather than argue about the cause

Stronger than bounding one function: **enumerate every path from your code into
the thing that moved, and show each is either baselined or bounded.** Across a
package boundary that is a `grep` for your own exported names, and it is
*complete* rather than suggestive.

The four errored tests included `generators/yield_from_deep_recursion.phpt` --
a generator test, in a session whose work is generators. The timeline exonerated
it and that was not good enough; it errors deterministically (3 of 3), so it is
not load noise either. What settled it:

| symbol | used in `runtime/interp` |
| --- | --- |
| `php.Generator`, `php.StartGenerator` | **none** |
| `php.NewDelegate`, `php.NewIter` | **none** |
| `php.Increment` | `incdec.go` -- baselined |
| `php.VarDump` | `program.go` -- baselined |
| `php.LookupBuiltin` | `callback.go` -- new, reaches 1 test, passes |

The interpreter has its own goroutine generators; the CPS drivers are reachable
only from compiled code, which this corpus does not exercise. So the question
closes **without identifying what did cause it** -- which on a three-writer tree
is often not determinable, and is a separate question from whether it was you.

## A first-blocker table is not a population count

`php2gotest`'s reason table reports the **first** gap that stops each file. That
is the right thing for triage and the wrong thing for ranking work, because it
understates anything that is commonly *later* in a file.

Measured both ways over the same 3,847 files:

| | first blocker | population |
| --- | --- | --- |
| any library function | -- | **2,532** |
| `var_dump` | 306 | **1,893** |
| catch handler | **415** | 706 |
| closure | 196 | 736 |

The ranking inverts. `var_dump` is understated six-fold, and the exception path,
which leads the first-blocker table, is a third the size of the builtin surface.
The reason is ordering: a file stopped at `catch handler` also needs a builtin,
and will hit it the moment catch lands.

**Use the first-blocker table to decide what to fix next only if nothing is
systematically early.** Otherwise count populations.

### And a population census can understate to zero

The census built to expose the above walked **instructions**, so gaps that refuse
a whole *function* -- catch handler, by-reference parameter, a resumable body --
counted zero. `catch handler` appeared nowhere in the first table it produced.

It was caught because it was conspicuously **absent** rather than merely low. A
present-but-small number would have been believed, which is the more dangerous
shape: absence is loud, understatement is quiet.

## `GATE UNSTABLE` conflates a moving tree with a flaky check

`hack/gate.sh` re-runs on failure and reports `UNSTABLE` when a failure does not
reproduce, which is the encoding of the rule above. Its branch has two cases: the
gate script changed mid-run, or "shared tree".

**A genuinely nondeterministic check lands in the second and is reported as
something to disregard.** Constructed rather than inferred -- a test in
`runtime/php` that fails on its first run and passes on its second gives:

```
GATE UNSTABLE: test did not reproduce -- shared tree (gate 4cae80f146ab); not a result
```

The tree was not the cause. The check was flaky, which is a real defect, and the
verdict says to dismiss it.

This is the same bucket problem as `php2gotest`'s `unsupported` one level in --
*we cannot compile it* versus *the oracle could not ask* there, *the tree moved*
versus *the check is nondeterministic* here. Both mix a gap with a non-result,
and in both the non-result reading is the flattering one.

It is not hypothetical in this repo: differential cases have already been shown
to depend on the engine **process** state (the `gc_collect_cycles` coupling,
where trimming one case changed another's answer). A check whose verdict depends
on what ran before it is exactly what produces a spurious `UNSTABLE`.

**The split is available by the move already used one level out.** The script
checksums *itself* to separate "the script moved" from everything else; checksum
the **input set** to separate what remains. If the tracked source is
byte-identical across both runs and the verdict flipped, nothing external
changed and the check is nondeterministic -- which wants its own verdict, since a
flaky check and a failing check call for different responses.

Implemented as `GATE FLAKY`, exit 3, with the input hash covering every `*.go`
and `testdata/` file and pruning `third_party`, `.agents-workspace` and `.git`.

### `GATE GREEN` means passed once, not passes reliably

The gate re-runs **only on failure**, so nondeterminism is detected in exactly
one direction. A check that fails then passes is `FLAKY`; a check that *passes*
then fails is never looked at. Constructed: a test that passes on its first run
and fails on every run after gives `GATE GREEN`, exit 0.

The risk is a reader over-trusting `GREEN` *because* the gate got more
sophisticated -- `FLAKY` existing suggests flakiness is covered, and it is
covered one way round.

Worth knowing if the other direction is ever wanted: **of the four checks, only
`test` can be nondeterministic.** `gofmt`, `build` and `vet` are pure functions
of the input set. So the asymmetry needs closing for one check, and
`go test -count=2` buys it for one extra test run rather than doubling
everything.

## On RED, ask whether the break can reach the measurement

"Measure only on a green tree" is a sound default, and the reason is that a break
can reach the measurement. **Whether it can is checkable**, and the two errors
are symmetric:

- Measuring *through* a break that reaches you. A mid-edit `runtime/php` that did
  not compile made 25 generated programs fail to build; the harness scored them
  `errored`, which was correct by its own definition and completely misleading,
  and the corpus run was void.
- **Discarding a result because of a break that does not reach you.** A `GATE
  RED` in `internal/bundlebuild` -- a go.sum failure in a module it generates --
  cannot touch a compiled-corpus measurement, established rather than assumed:

  | | |
  | --- | --- |
  | a generated program imports | `runtime/php`, and nothing else |
  | `go list -deps ./runtime/php \| grep -c protobuf` | **0** |
  | the failure is about | protobuf go.sum entries |
  | `php2gotest` itself | built and ran |

So the rule: **on RED, establish whether the failing component is in the
measurement's dependency path, and say which, with the evidence.** Defaulting
either way costs a round.

Note what this does *not* license: it is a claim about the dependency path, not
about whose the break is. On a tree with three writers, "not in my path" and "not
mine" are both weaker than they sound, and neither identifies an author.

### Compose the claim after reading the result, not in the same command

Reporting the above, the sentence *"my gate was RED before and after the run"*
went out in the same shell invocation that ran the after-check. The check said
`GATE GREEN`. The claim had been written before its own evidence existed.

Not a reasoning error -- nothing was inferred wrongly. The shape of the command
put the assertion *before* the observation, which is the same shape as
`echo "gate clean"` on the line after a pipeline: the text and the check in one
breath, and only one of them connected to reality.

The fix is structural, not resolving to be careful: **read the result, then write
the sentence.** Never bundle a check and a claim about that check into one
command.

### The evidence is right; the connective tissue is invented

Three times in one day, the same shape -- and it is worth stating as a specific
failure mode rather than a general caution, because the specific form says where
to look:

| evidence (correct) | step attached (invented) |
| --- | --- |
| `runtime/php` builds clean | therefore the RED is **theirs** |
| a gate check about to be run | "RED before **and after**" |
| 27 protobuf packages in `runtime/interp`'s deps | **via `runtime/bundle`, today** |

Every observation was true. Every clause joining it to a conclusion was
unchecked. And in all three the invented step is precisely the one that makes the
finding *interesting*: theirs rather than merely not-mine, both-ends rather than
just-before, new-today rather than always-true.

So the thing to check is not the observation. It is **the clause that makes the
observation worth sending.**

The third one has a traceable origin worth keeping, because it looks like
reasoning and is not. The go.sum diagnostic read:

```
runtime/interp/class.go:4:2: missing go.sum entry for ... protobuf/proto
    (imported by github.com/moriyoshi/php2go/runtime/bundle)
```

That was read as `interp -> bundle -> protobuf`. It does not say that: the *file*
needing the entry and the *module* being resolved are different fields of one
compound message, collapsed into a dependency edge that does not exist.
`go list -deps ./runtime/interp` then showed 27 protobuf packages -- true, and
**equally consistent with the wrong story and the right one** (they arrive via
`internal/ir/irpb`, which 23 interp files have always imported). The confirming
measurement did not discriminate.

Which is the probe rule again: *what else would have produced this result?*

The general form, from the compiler side, and it covers all of it: **assertion
and observation separated by intent rather than by structure.** Four surfaces so
far -- a shell banner, the verification of the fix for that banner, prose about
tooling state, and causal claims. Intent does not survive contact with a sentence
that is more interesting if it is true.

## A single GATE RED on a shared tree is not a result

`hack/gate.sh` computes its verdict from the checks, which makes it honest about
the tree -- including the moments when the tree is genuinely inconsistent because
two sessions are writing it.

Observed over about five minutes with two sessions active: `GATE RED: test`,
`GATE GREEN`, `GATE RED: gofmt vet test`, `GATE RED: gofmt`, then four
consecutive `GATE GREEN`. Every RED was transient and resolved on a re-run --
`gofmt` catching a file between writes, `vet` and `test` catching a package
mid-edit.

This is not a false positive. It is a correct report of a real state, and the
weaker hand-typed gate could not produce it only because its banner did not
depend on the checks.

**So the gate needs the rule the corpus already has.** *A measurement with
compile errors is noise* exists because the **box** is shared; this is its
sibling for when the **tree** is shared:

> Re-run before believing a RED. Treat it as real only if it reproduces.

Corollary, and it matters when two sessions edit one checkout: **a single RED is
not evidence that the other session broke something.** Three separate apparent
breakages -- a stale golden, a missing struct field, a RED gate -- were all
mid-edit transients that resolved unprompted.

### Reproduction across space is not reproduction across time

The worked example, and it happened within an hour of the rule above being
written, to the person who wrote it.

Testing whether the other session's cwd-anchoring fix worked, the first
observation said it did not: `GATE RED` from the repo root but `GREEN` from
`internal/` and from `cmd/php2lower/`. **Three directories, consistent with each
other** -- and consistent with the prior, since the bug being tested for was one
this session had found and reported. One message away from telling them their fix
did not work.

What stopped it was an unfamiliar message, `GATE UNSTABLE`, which the other
session had not mentioned -- so the script was being *written* while it was being
read. Re-run: `RED` from every directory. Third run: the same, with the script's
checksum stable.

The first observation was of a **mid-edit script**, and testing four directories
was reproduction across *space*, which on a shared tree is the axis that does not
matter. The one that matters is *time*.

Two lessons fall out, and the second is the uncomfortable one:

- A shared-tree observation needs a second reading **separated in time**, not a
  wider sample in one instant.
- **The confirming observation is the dangerous one.** A result that disagrees
  gets interrogated; one that matches what you already believe ends the
  investigation. The near-miss here was not a surprising result being trusted --
  it was an *expected* one.

## When two arms disagree, suspect they were given different inputs

Three times now the compiled path has differed from the engine and looked
exactly like a compiler bug, and three times the cause was that the two arms
were not given the same program:

| symptom | cause |
| --- | --- |
| `bug36037` wanted 1 and 6, we produced 2 and 7 | `php2eval` stripped `<?php` and shifted every line by one |
| every diagnostic differed in the filename alone | the engine reports `php2go-eval`; a compiled program reports its own path |
| a compile-time warning failed in `testdata` and passed on the identical file in `/tmp` | Zend resolves the path when it compiles, so the diagnostic carries the **absolute** path while the oracle was handed a relative one |

**Check the inputs before reading the diff as a semantics bug.** A one-line
difference in position, filename or line number is far more often a harness
asymmetry than a wrong answer.

Related, and settled by probe: the engine **always canonicalises**. Compiling one
file by a relative path, an absolute path, and a path containing `..` produces
the identical absolute path in the compile-time diagnostic every time. It does
not echo what it was handed, so normalising the oracle's input is not enough on
its own -- both arms have to be normalised to the same thing.

## A guard that cannot distinguish the failure it exists to catch

The `UNRENDERABLE` hole was a comment asserting that a build would fail. Its
sibling: a *check* that passes in exactly the case it was written to detect.

Messages to the other session were sent with `tmux send-keys "$msg"` then
`send-keys Enter`, which races the receiving TUI's paste handling; when it loses,
the text sits unsent in the input box. The verification was `capture-pane |
grep -c "$text"` -- and text sitting **in** the input box appears in
`capture-pane` just as happily as text that was submitted. The guard would have
reported success in the failure case. It did fail at least once, and was caught
by eye rather than by the check.

The working check is the negative one: verify the text is **gone from the input
box**, and separately that it reached the history. Retry, and fail loudly.

Generally: for any guard, ask *what does it print when the thing goes wrong?* If
the answer is the same as when it goes right, it is a comment.

### Three ways a guard can look like verification and not be

| | what it was | how it failed |
| --- | --- | --- |
| a comment asserting a property | `/* UNRENDERABLE */ php.Undef()` under a comment saying this is invalid Go so the build fails | it is valid Go; programs built and ran with holes |
| a check that cannot distinguish the failure | `capture-pane \| grep -c "$text"` to confirm a message was sent | unsent text in the input box matches exactly like sent text |
| **a guard that bites, but only on the happy path** | `TestNoBuildMachinePathReachesTheArtifact` -- real, and mutation-checked | it only exercised a file *inside* the compilation root, and the fallback to an absolute path only fires *outside* it |

The third is the subtlest because it survives the usual check. Mutation-testing
asks *does it fail when I break the code*; it does not ask *does it run on the
input where the code is hard*. Both questions are needed, and the second one is
about the **input set**, not the assertion.

That leak was found by following a design through, not by testing -- the `../`
case read as an aesthetic detail when it was raised, and it was the only input
that exercised the fallback at all.

### Knowing the failure mode does not protect you from it

The gate command below was written *after* the table above, by the person who
wrote the table, in the session that produced it:

```sh
go test ./... 2>&1 | grep -v "^ok" | head -3; echo "gate clean"
```

The `echo` is unconditional. It printed `gate clean` in the same breath as a
package that failed to build.

These failures are not caused by not knowing about them. They are caused by the
fact that **the broken shape is the convenient shape**: grep for problems, print
a banner, and the banner is not connected to the grep. Vigilance is not the
defence, because vigilance is what just failed.

The defence is structural: **make the success message a function of the check**,
so there is no way to print it without having passed. Same argument as a gate
beating a rule, turned on the person applying it.

And the sharper form, from the compiler side, who hit the bug **inside the
verification of its own fix** -- checking a new exit code with
`hack/gate.sh 2>&1 | tail -2; echo "exit=$?"`, which reads *`tail`'s* status:

> Species (2) is not a bug you fix once in the code. It is the shape of the
> convenient shell command, so it recurs in the **checking** as readily as in the
> thing checked.

Which is where nobody is looking, because the checking is the part you trust.

### A probe that agrees with you may be agreeing for a reason you have not checked

Proving one gate bug took three experiments, and the first two would each have
supported a confident wrong conclusion:

| probe | said | actually |
| --- | --- | --- |
| 1 | GATE RED -- so the gate works | the probe lived under `.agents-workspace/`, and `./...` **skips dot-directories**, so the tool never saw it |
| 2 | GATE RED -- so the gate works | right answer, wrong reason: `go test` re-runs a *subset* of vet, and `printf` is in that subset |
| 3 | **GATE GREEN with two findings on screen** | `copylocks` is outside the subset -- the actual hole, and much narrower than "the gate is broken" |

Only the third could separate *sound* from *broken, but not for the reason you
think*.

**The danger is worst when a probe agrees with you, because agreement ends the
investigation.** A disagreeing probe gets interrogated; a confirming one gets
believed. So the question to ask of a confirmation is the same one to ask of a
passing check: *what else would have produced this result?*

## `php2gotest`'s `unsupported` bucket is two populations

The compiled harness grades with `php2eval`, which **evals** the file. The
interpreted harness runs it. That difference is not neutral: a test whose
expected outcome is a *fatal compile error* makes the engine bail, `php2eval` has
nowhere to put that, and the file is scored `unsupported` -- while `php2phpt`
runs the same file and passes it.

`Zend/tests/030.phpt` is the clean case: `catch (Exception $this)`. Interpreted,
it passes. Compiled, it is `unsupported: engine could not run the file`.

Measured over one full run, aggregating the reported reason table: **23.1% of
the reasons are the oracle or the harness, not a gap in php2go** -- 844 of them
the bail above.

This matters most for the thing it hides. 28% of the corpus expects a
`Fatal error` or `Parse error` line and 37% expects some diagnostic, so the
population the oracle silently drops is disproportionately the population that
tests diagnostics -- which is already the single largest known gap in compiled
output. **Do not read a low `failed` count on the compiled path as evidence that
diagnostics are a small problem.** Most of the tests that would say otherwise are
in `unsupported`, and not because php2go cannot compile them.

Related, and from the compiler side: the engine reports the filename
`php2go-eval` where a compiled program reports its own path, so any test whose
expected output contains a filename cannot pass regardless of correctness. Also
harmless only until diagnostics are emitted.

## A verifier checks shape; only a second consumer checks meaning

`php2lower -verify` could never have caught the `true`/`false` defect, and the
reason generalises.

Both boolean literals lowered to the operand text `bool`, so `$t = true` and
`$f = false` were **the same instruction**, indistinguishable to every consumer.
The harvest never lost the distinction -- `IS_TRUE` arrives as `Int 1` -- the
operand renderer discarded it. Every structural invariant still held: the IR was
well-formed, every value dominated its uses, every block had a terminator. It was
**structurally perfect and semantically lossy**, and `bool` reads as a type
annotation right up until you write a consumer that has to produce a value from
it.

A verifier can only check the properties you thought to assert about a
representation's *shape*. What it cannot check is whether the representation
still means what the input meant, because it has nothing to compare against. A
second consumer does: the interpreter and the emitter derive different things
from the same IR, and where they disagree the IR is ambiguous.

So an emitter is not only a code generator. It is:

- **a checker for IR properties no invariant asserts** -- Go refused an
  unreachable label and thereby found the dead block both sides had filed as
  cosmetic;
- **a differential oracle for the IR itself** -- anything two consumers must
  agree on and do not is a defect in what sits between them.

Practically: when an IR change lands, the question "does `-verify` still pass" is
weaker than it sounds. Ask what *reads* the changed field and whether more than
one thing does.

Three instances of the category so far, all found by a consumer and none
findable by an invariant:

| lossy step | what was lost | found by |
| --- | --- | --- |
| `true` and `false` both render as the operand text `bool` | which boolean | an emitter that had to produce a value |
| `ZEND_COALESCE` lowered to `copy $q` + `br.null` | that the read does not warn | the undef-warning emitter |
| `.suspends_to_caller` set per suspension point | that it must survive `yield from` | a driver walking a delegation chain |

**And the assertion in a source comment is the same failure with no consumer at
all.** The compiler side emitted `/* UNRENDERABLE: x */ php.Undef()` under a
comment stating that this was deliberately invalid Go so the build would fail
loudly. It is valid Go, so programs built and ran with holes in them. A comment
claiming a safety property is a test that was never written, and it reads as
documentation -- so it is worse than no comment, which at least prompts the
question.

## `Zend/tests` is not one program, and some counts over it are inventions

A whole-corpus number computed by *merging declarations across files* is
measuring a program that does not exist. `Zend/tests` is ~5,000 independent
files, each its own process, and names repeat freely: **16 files declare their
own `class It implements IteratorAggregate`**, with differing `getIterator`
bodies. A name-keyed class
map collapses them into one. The compiler side found this the expensive way --
a `Descendants` walk over a name-keyed parent map went to 119 GB and an OOM
kill, because `A extends B` from one file and `B extends A` from another make a
cycle no program has.

CakePHP does not have the problem: it is one program, so its declarations really
do belong to one namespace.

The test to apply to any corpus number, and it is the same shape as the
load-invariance question one dimension over:

| | survives merging | example |
|---|---|---|
| a per-body syntactic count | yes | `.resumable` functions, `ret.suspend` sites, the operand shape of a `yield from` |
| a per-file outcome | yes | `.phpt` passed/failed/unsupported/errored -- each is its own process |
| anything needing a **relation between declarations in different files** | **no** | call-graph edges, class hierarchy, "which `getIterator` does `new It` reach" |

So: count within a body freely; the moment a number needs two declarations to
refer to each other, ask whether they were ever in the same program.

**Then check which column you are discarding.** Applying this test, the runtime
session put CakePHP's ceiling (41.3% -> 33.8%) into the *discarded* list on the
belief that it was a `Zend/tests` figure. It is not: that row is 41.3% CakePHP
against 15.7% `Zend/tests`. The caveat kills the `Zend/tests` call-graph column
-- 4.5%, 6.1%, 15.7%, edge counts -- and leaves every CakePHP figure standing.
The slip discarded the number the whole selective-CPS cost argument rests on
while keeping the one that had to go, which is worse than not applying the
caveat at all, because it looks like diligence. Deriving a rule and applying it
to the right column are separately fallible; only the first one feels hard.

## Label a property checked, not proven, unless you checked the structure

The tempting way to justify a property is a structural argument that sounds
inevitable. "A suspension terminator ends its block, so a continuation is a
fresh leader and any join lands in a later block" is one, and it is false: a
fresh leader can still have other in-edges, and `counter`'s bb6 is a dispatch
target *and* the false arm of the `if` above it.

The assertion built on that reasoning was correct anyway. The reason was not,
and the gap between them is where the next wrong conclusion comes from -- a
property believed for a structural reason gets extended to the next case the
reason seems to cover, and a property known from a corpus sweep does not.

So:

- Prefer the sweep. 335 rewritten bodies with a permanent guard is a stronger
  claim than a paragraph of CFG reasoning, and it keeps holding as the compiler
  changes.
- When both are available, state which one you have. `TestX` asserting a
  property over a corpus is "checked"; a property derived from block structure
  is "proven" only if the structure was actually examined rather than imagined.
- **The pattern to watch for is reaching for a structural argument instead of
  checking the structure.** Five conclusions in the CPS handoff were right for
  reasons narrower than stated, all five the same shape. The cost is not the
  wrong conclusion -- there wasn't one -- it is that the stated reason then gets
  reused somewhere it does not hold. See the fiber call-graph correction in
  JOURNAL.md for one that did get reused, twice, before anyone checked it.

### Why the conclusions survived and the reasons did not

The useful half, and it is not "reason more carefully". Across five rounds every
*conclusion* was anchored to something checkable -- a corpus count, an
invariant, a benchmark -- and every *reason* was prose that nothing tested. The
conclusions held because the anchor held; the reasons drifted because nothing
could catch them.

So the rule is about what a claim is attached to, not about how hard it was
thought about:

| Claim | Anchor | Outcome |
|---|---|---|
| "a continuation that uses its value has one predecessor" | 335 bodies, guarded by `php2lower -cps` | held |
| "a continuation is a fresh leader so joins land later" | none | false, `counter`'s bb6 |
| "fibers are 160x more expensive per transfer" | a benchmark | held |
| "a whole-program rewrite needs a call graph" | none | false, uniform CPS asks nothing |
| "dual entry needs a static discriminator" | none | false, the flag can be dynamic |

**A claim with no check attached is the one that turns out to be wrong.** When a
supporting argument cannot be given an anchor, say the conclusion is *checked,
not proven*, and let the check be the thing anyone relies on.

## The third arm: grading emitted Go

`php2eval` compares the engine against the interpreter in one process. Emitted
Go cannot be graded that way: it has to be written out, compiled, and run.
`php2gotest` is that arm.

```sh
go build -o /tmp/php2eval ./cmd/php2eval
php2gotest -php2eval /tmp/php2eval third_party/php-src/Zend/tests
```

Buckets, and the reason they stay apart:

| | |
|---|---|
| `passed` | the generated program's stdout is byte-identical to the engine's |
| `failed` | it ran and disagreed — a semantics bug |
| `unsupported` | the emitter refused, by name — a known gap |
| `errored` | Go rejected our own output — a compiler bug |
| `noreference` | the oracle could not produce a reference output — **not about php2go at all** |

### The compiled path has its own differential corpus

`cmd/php2gotest/testdata` holds small cases that must all pass:

```sh
go build -o /tmp/php2eval ./cmd/php2eval
go run ./cmd/php2gotest -php2eval /tmp/php2eval cmd/php2gotest/testdata
```

**No expected output is written down.** It comes from the engine at grading
time, so the cases cannot drift into recording what php2go does — the same
reason a golden regenerated by `-update` is not evidence about behaviour.

### `noreference` is not a php2go gap, and merging it into `unsupported` lies

The runtime session found this and it is the sharpest caveat on the number.
`030.phpt` is `catch (Exception $this)` — a **fatal compile-error test**. The
engine bails at compile time by design; `php2eval` evaluates a string and has
nowhere to put that outcome, so the file scored `unsupported` and read as
something php2go cannot compile. **`php2phpt` runs the same file and passes it.**

844 files were in that state. Counting them as gaps overstates what the compiler
cannot do, and it hides where the compiler is weakest, because the tests the
oracle cannot ask about are disproportionately the *diagnostic* tests — which
are also the ones the emitter fails. Independently counted from both sides:
**1,438 of 4,966 corpus files (29%) expect a `Fatal error` or `Parse error`
line, and 1,853 (37%) expect some diagnostic.** Over a third of the corpus tests
diagnostics, php2go emits none, and most of that never reaches the comparison.

So: **do not read a low `failed` count as evidence that diagnostics are a small
problem.** It is a measurement that hides most of itself. Report the rate over
the files the oracle could actually grade, which `php2gotest` now prints as a
second table.

**`unsupported` and `errored` are the load-bearing pair.** A refusal is a gap we
know about; Go rejecting our output means the emitter is wrong about Go. Letting
the second hide inside the first is how a compiler acquires silent holes, and it
happened immediately: emitting `var_dump` as a call to a Go function that does
not exist put a known gap into `errored`. Library calls are now refused by name.

### Both arms must see the same source

The oracle **evaluates a string** and the compiler **compiles a file**, and that
difference is a source of false failures rather than a detail:

- **Line numbers.** `zend_eval_string` gets the body with the opening tag
  removed, so dropping `<?php\n` shifts every line up by one. `bug36037.phpt`
  wanted `1` and `6`, the compiler produced `2` and `7`, and it read as a
  compiler bug for as long as it took to look. `sameLines` now removes the tag
  and keeps the line it occupied. The tag cannot merely be blanked: text before
  an opening tag is output, so that would make the file inline HTML.
- **Filenames, still unfixed.** The engine reports `php2go-eval`; a compiled
  program would report its own path. **Any test whose expected output contains a
  filename cannot pass**, independently of whether the compiler is right. This
  does not bite yet only because the emitter produces no diagnostics at all, and
  it will bite the moment it does.

Read a `php2gotest` number with that caveat attached, and never quote its
`failed` count as a semantics-bug count without checking the failures do not
turn on a filename or a line number.

## A golden written by `-update` records what the code does, not what PHP does

`TestGoldenIR` pins the *textual format* of the IR, and that is all it is for.
It is not evidence about behaviour, and regenerating it with `-update` is not a
verification step — it is an assertion that whatever the code currently produces
is correct.

2026-08-29 made the difference concrete. Regenerating one golden for an intended
change also rewrote `finally.ir` and `foreach.ir`, which had been stale in the
working tree. Reading the diff rather than just re-running the test turned up
that the **old** golden encoded a real bug: a `finally` body's source position
was attached *before* the position of the `return` expression that preceded it.
The engine says otherwise —

```php
try { if ($early) { return $undef_in_return; } } finally { echo "finally ran\n"; }
```

prints `Warning: Undefined variable $undef_in_return ... on line 4` and *then*
`finally ran`, so the return expression is evaluated at its own line before the
finally runs. The regeneration happened to fix it. **Happening to be right is
not a method**, and the golden would have been equally happy recording the bug.

So:

- **A golden pins format. Behaviour is pinned by a test whose expected value
  came from the engine.** `TestAReturnExpressionIsAttributedBeforeTheFinallyBody`
  is that test for this case, with the probe in its comment.
- **`-update` accepts every pending change in the tree, not the one being
  made.** Read the whole diff; a change that rides along is indistinguishable
  from the one you meant.
- This is the project's rule about hand-written expected output, one level up:
  a golden regenerated from the implementation is the implementation asserting
  its own correctness.

It became load-bearing only when the emitter began rendering `.line` as
`ctx.SetLocation` and diagnostic positions started being graded byte-exact —
which is the general shape worth remembering: **a latent defect in the IR is
invisible until a consumer needs the part of it that is wrong.**

## A probe that confirms your hypothesis may be confirming it for a reason you have not checked

Established 2026-08-30 while proving a gate was broken, and sharper than the
guard taxonomy it came out of.

The claim under test was "my gate prints GATE GREEN when `go vet` finds
something".

1. **Probe 1 said `GATE RED`.** The gate looked sound. It was not even the right
   experiment: the probe was written under `.agents-workspace/`, and `./...`
   skips dot-directories, so the go tool never saw it.
2. **Probe 2, in a real package, also said `GATE RED`** — for the wrong reason.
   `go test` re-runs a *subset* of vet, and `printf` is in that subset, so the
   verdict came from a check that was never in question.
3. **Probe 3 used `copylocks`**, which `go test` does not re-run. `GATE GREEN`
   printed with two vet findings on screen.

Only the third could separate *sound* from *broken, but not for that reason* —
and the first two each supported a confident wrong conclusion.

**The danger is worst when the probe agrees with you, because agreement ends the
investigation.** The first `GATE RED` was the wrong RED, and stopping there was
the tempting move. This is the same failure as reading a passing check without
asking what it prints when things go wrong, except the check is your own
experiment.

So: before believing a probe, say *which* mechanism produced the result, and
construct the case where that mechanism is absent.

## Composing a claim in the same breath as the check that would justify it

The shell version of this is species (2) -- `echo "gate clean"` on the line after
a pipeline, saying the same thing whether or not the pipeline found anything.
The prose version is the same defect and is harder to see, because there is no
code to inspect:

```sh
hack/gate.sh > /tmp/g 2>&1; echo "$(tail -1 /tmp/g)"
send-message "... Gate green; corpus 86/46/3778/0/1076 ..."
```

Both sessions did this repeatedly. The message text asserting *gate green* is
written **before** the gate has produced its answer -- the command runs the check
and the claim in one breath, and only one of them is connected to reality. The
runtime session caught themselves reporting "RED before and after" when the
after was GREEN; the assertions on this side happened to be true every time,
which is luck rather than method.

It is not a reasoning error. Nobody inferred anything wrongly. **The shape of the
command put the assertion before the observation.**

The fix is structural, as it was for the banner: **compose the claim after
reading the result, in a separate command.** Never write a sentence about a
check's outcome in the same invocation that runs it.

That the same defect appears in a shell banner, in a verification of the fix for
that banner, and in prose about someone else's tooling suggests it is not really
about any of those. It is about assertion and observation being separated by
intent rather than by structure.

## Enumerate every path from your code into the thing that moved

The runtime session's method, and better than arguing about whether a change
caused a movement.

Their acceptance criterion was "the interpreted corpus must not move at all".
On a three-writer tree that rule is unusable as stated, because someone else
moving the number looks identical to you moving it — and applying it literally
would have reverted correct work. What they did instead:

- **Bound the reach.** Only one corpus file uses `array_reduce`, so the change
  could move at most one test. Four moved, so the movement is *arithmetically*
  outside what the change can reach.
- **Enumerate the symbols.** `grep` for every exported name of theirs across the
  package that moved. `php.Generator`, `php.NewDelegate`, `php.StartGenerator`
  and `php.NewIter` have **zero** uses in `runtime/interp` — it has its own
  goroutine generators — so a generator test that errored could not be theirs
  whatever the timeline said.

That is **complete rather than suggestive**: if none of your symbols is
reachable, the question is closed without needing to identify what did cause it,
which on a shared tree is often impossible.

So the usable form of an acceptance criterion is not *the number must not move*
but **the number must not move because of me**, and that needs a reachability
bound rather than a policy. Same shape as asking whether a RED gate is in the
measurement's dependency path, one level over — from *can the break reach my
measurement* to *can my change reach the number*. Both replace a policy with a
measurement.

## Do not run a blind pattern substitution over structured code

2026-09-01, `internal/gogen`. Routing every fallible call site to a new
error-handling helper was done with a regex over `fmt.Sprintf` calls. It
rewrote the format strings correctly and put the new argument **first** in every
call, where its `%s` sits in the middle — silently misordering the arguments of
ten call sites.

`go vet` caught **four** of them, the ones where the argument *count* also
changed. The other six had the right count and the wrong order, which vet cannot
see because every argument is a string. Those were caught by the compiled corpus
going 12/12 → 4/12.

Two things worth keeping:

- **The layered checks did their job in order.** vet caught what vet can catch,
  and the differential corpus caught the rest. Neither alone was sufficient, and
  the corpus is what turned "it compiles" into "it is wrong".
- **A substitution that edits both a format string and its arguments is not a
  substitution**, it is a refactor, and it needs to be done per site. The regex
  was faster to write and cost far more than the ten manual edits would have.

## The baseline is the run immediately before your change

The third and last piece of the acceptance rule, from the runtime session.

Their last three interpreted runs were `4362/195/154/2`, then `4363/195/151/4`,
then `4363/195/151/4`. Compared against the **oldest** they would have reported a
movement and had to re-litigate causality; against the run **immediately
before** their change, on the same tree, it is exactly unchanged.

On a tree three sessions are writing, a remembered baseline silently accumulates
everyone else's work into your diff. So the complete rule is three measurements,
none of them a judgement:

1. **Bound the reach before running.** How many corpus files can this change
   touch at all? `array_reduce` reached one, so the criterion could detect
   almost nothing and the conformance tests carried the weight;
   `set_error_handler` reached 135, so an unchanged number means something.
2. **Compare against the run immediately before**, on the same tree.
3. **If it still moves, enumerate your symbols** in the package that moved. Zero
   reachable uses closes the question without identifying the cause.

Each replaces a judgement with a measurement, and together they cover the three
ways this went wrong in one day: a criterion that could not detect anything, a
movement that could not be attributed, and a baseline that was not the right
comparison.

## When reading a value back from the engine, vary the value and not only the condition

Three instances in one day, all the same shape: a table read back from the
engine, **correct for every input it was tested with and wrong for a class of
inputs it was not**.

| table | conditions covered | input axis assumed |
|---|---|---|
| `InitThrowable` properties | the object's shape | asserted names and order, not the *rendered* form |
| `ArrayKey` coercions | int, string, bool, null, float | no `NAN`, no infinities, no overflow |
| the diagnostics table | nineteen conditions | one benign **name** each |

The last is the clearest. Every message was read back from the engine, which is
right — but always with an unnamespaced, quote-free, percent-free name. So four
constructors used Go's `%q` on a name that can contain a backslash, and the
probe and the bug lived in **different inputs rather than different code**.

Probing it turned up something sharper than the backslash: **PHP escapes
nothing.** `Undefined array key "say "hi""` — it wraps the bytes in a quote
character and stops. Go's `%q` escapes backslashes, escapes quotes, and would
escape non-printables. So the rule is not "backslashes need care", it is **`%q`
is not PHP quoting and never was.**

And the obvious fix — concatenating the quotes into the format — is wrong a
second way that `go vet` catches: a name containing `%` is then read as a verb.
The name has to reach the message as an **argument**, with the quotes literal in
a constant format.

So a conformance table needs two axes. Nineteen conditions with one value each
is one axis exercised and one assumed. Vary the value: a namespaced name, an
embedded quote, a percent, a boundary number, a NaN.

## A cluster is only a lever if its members share an implementation

Ranking gap clusters by "files this would unblock" has a second failure mode,
distinct from ranking by frequency.

2026-09-01: a cluster called "this/misc" topped the census at **372 files
unblocked** -- ahead of everything else. Its members individually unblocked 12,
44, 29 and 3. The 372 was a **union effect**: files blocked on two or three
unrelated leftovers at once, which closing *all* of them would free and closing
any one of them would not.

The contrast is the cluster ranked just before it, "magic + readonly" at 573.
That one was real, because its members -- `__get`, `__set`, `__call`,
`__callStatic`, readonly, property hooks -- are **one implementation**: the same
`visible()` predicate, the same re-entry guard, the same access paths. Doing the
first made the rest nearly free.

So before treating a cluster as a work item, ask whether closing it is one piece
of work or several. If several, the honest number is the largest single member,
not the union.

**The converse is also worth doing:** when a cluster fails the test, look inside
it for a group that *does* share an implementation. `ANY library function` at 909
files is a flat tail whose largest member is worth 16 -- but the class-table
group inside it (`get_class`, `class_exists`, `method_exists`, `class_alias`,
`property_exists`, ...) is one implementation over one table and was worth 56.

## A partial result read as a complete one

Three instances in two days, in three different tools, all with the same shape:
**a measurement that could not have shown the thing it was taken to rule out.**

| what was read | why it could not answer |
|---|---|
| `grep ... \| head -8` on a getter list | the list is twelve long; the answer was in the tail |
| a corpus run that exited 0 with no summary | it had panicked at file 722 of 4,986, with stderr discarded |
| a conformance table with one value per condition | every row tested, one input axis assumed |

Before reading a result as negative evidence, ask what it would have looked like
if the thing were present. `head -N` on a listing whose *length* is the answer,
and `2>/dev/null` on a run whose failure mode is a crash, are both guaranteed to
look clean.

## To choose work, rank by sole blocker

The census dump gives each file's whole gap set. Three ways to read it, in
increasing order of usefulness:

1. **Frequency** -- how many files mention this gap. Nearly useless: `closure`
   appeared in 708 files and unblocked 36.
2. **Cluster-unblocks-alone** -- files whose entire gap set is inside a candidate
   cluster. Better, but a cluster only counts if its members share an
   implementation, and a union of unrelated leftovers ranks high for nothing.
3. **Sole blocker** -- files where this single gap is the *only* thing left. This
   is the one that predicts conversion.

2026-09-02, with 2,675 files still blocked: **1,529 blocked by exactly one gap**,
608 by two, 538 by three or more. Against that distribution, closing a gap
converts roughly its sole-blocker count and moves everything else one refusal
along. Measured: enums, 110 sole blockers, converted 25 (28%); dynamic classes,
picked from frequency, converted 2.

## A ratio is only as honest as its denominator

2026-09-03. The specialization rate read **0.2%**, and the mechanism looked
inert. Classifying what was being counted showed that **89% of the denominator
was operations with no specialization to reach** -- `concat` and the comparisons
have exactly one variant. Against the calls that could actually move, the same
run reads **3.2%**.

The number was wrong in the *pessimistic* direction, which is the direction that
usually escapes scrutiny: a bad result gets explained rather than audited. The
rule is symmetric with the one about flattering numbers -- **before reading a
ratio, ask what is in the denominator that could never have moved.**

Corollary, learned the same day: when a prediction about a number fails,
*measure why before trying the next idea*. Classifying the operands took one
throwaway program and showed that places were 10% of them, so the change just
made could never have been the lever it was claimed to be.

## Read our own output by running the emitted program

`php2gotest`'s diff line truncates, so `got "Fatal error: Unca..."` says almost
nothing. Three attempts went into inferring the rest of a message from its first
twenty characters before the obvious move: keep the generated Go and run it.

    php2gotest -keep <dir> -root <root> <file>
    cp <dir>/*.go .agents-workspace/tmp/prog/main.go
    go run ./.agents-workspace/tmp/prog

The copy into the repo's own module is what makes it cheap -- the emitted
program imports `runtime/php`, so a scratch directory outside the module needs a
`go.mod` with a `replace`, and `-keep`'s filename is the mangled source path
rather than something `go run` will accept.

With both arms in full, the return-type case showed **two** differences at once
-- a wrong position *and* a missing trace frame -- where the truncated line
showed neither.

## A cluster whose first failure is a position hides the rest

`type_error_position.php` failed four times on four different lines, and each
fix exposed the next defect: the raise position, then the lowercased function
name in the trace, then an unrefused scalar-against-class-type, then an
unqualified method name.

None of the last three was visible while the first was wrong, because the test
never reached the line that showed them. So **the number of tests a cluster will
fix cannot be read off the diff** when the first differing line is a position, a
prefix, or anything else that every later line depends on. Fix, re-run, read the
new first difference -- and do not estimate the cluster's size from the old one.

## A zero-byte `failed` on a file that compiles is not a missing feature

`$o++` on an object graded `failed` with **0 bytes** of output, which is the
silhouette of something unimplemented -- and it compiled fine. `runtime/php`
reports "cannot step this value" as a *sentinel*, because naming the class in
`Cannot increment Foo` needs a class table it does not have, and the emitter
never mapped it. The sentinel escaped as a Go error and killed the program
before it printed anything.

This is the mirror of the rule the quality gate is built on. A gap must never
masquerade as working behaviour -- and here working behaviour masqueraded as a
gap, which is expensive in its own way: while clustering, a zero-byte failure
reads as a large feature and gets skipped for something that looks closer to
done. Four tests sat untouched for exactly that reason.

The tell is one command:

    php2gotest -keep <dir> -root <root> <file>
    cp <dir>/*.go .agents-workspace/tmp/prog/main.go && go run ./.agents-workspace/tmp/prog

- `php2go: <something>` on stderr, exit 255 -> a **translation hole**. Some
  error reached the top with nobody to turn it into a PHP condition.
- PHP output that differs from the engine's -> an ordinary wrong answer.
- Genuinely no output and a clean exit -> the feature gap it looked like.

## `go build ./...` passing says nothing about the emitted program building

Renaming a parameter in an emitted helper left one reference to the old name
behind. php2go compiled perfectly -- the emitter is fine, the mistake is in the
Go it *writes* -- and every generated program failed with `undefined: v`.

    files 85   passed 0   failed 0   errored 85

This is the failure mode specific to writing a compiler, and the local
differential corpus is the only thing that catches it:

    php2gotest -php2eval <bin> -root cmd/php2gotest/testdata cmd/php2gotest/testdata

Run it after **every** emitter change, not only before making a claim. The
whole-corpus run is too slow to serve this purpose and the Go gate cannot serve
it at all.

A total break is the lucky shape: 85 identical `errored` lines are unmissable.
A change touching only some emission paths gives a handful of odd failures
mixed into the real ones, which reads like a bad fix rather than a typo.

## For a broad change, diff the failing sets rather than reading the total

`Generator.step` is on the path of every generator advance; a stack-frame change
there moved the corpus by **+1**. That single number is equally consistent with
"one test fixed" and "three fixed, two silently broken", and nothing in the
totals distinguishes them.

    grep -h '^failed ' <before>[01][0-9].txt | awk '{print $2}' | sort > before.txt
    grep -h '^failed ' <after>[01][0-9].txt  | awk '{print $2}' | sort > after.txt
    comm -23 before.txt after.txt   # now passing
    comm -13 before.txt after.txt   # newly failing

Do the same over the `unsupported` sets when that count moves: a file entering
it is a wrong answer becoming an honest refusal, and a file leaving it is
coverage genuinely added. Both are worth naming, and the direction alone does
not say which happened.

**When a change is broad and the movement is small, the set difference carries
the information the aggregate does not.**

## A refusal that quotes a value must say what an empty one means

`unrenderable operand ` sat at the head of the census for weeks -- 80 files,
the largest compiler-side cluster -- and the reason ended mid-sentence because
the operand it quotes was **empty**. Nobody could read it, so nobody chased it.

Two rules out of that:

- **Name the instruction, not only the operand.** A refusal raised where there
  is no error return -- `operand()` is the one -- has to be told which opcode is
  being rendered, or the reason describes a fragment. The emitter now parks the
  opcode per instruction for exactly this.
- **`e.op` is stale in a terminator.** A terminator renders after the block's
  last instruction, so a refusal there names that instruction. Reading
  `empty operand to frame.set_state` as "frame.set_state is broken" is wrong:
  that opcode never calls `operand()` at all. It was the terminator behind it.

## A correctness fix can be broad and still move nothing

`instanceof` never matched an interface -- every `$x instanceof SomeInterface`
was false and `catch (SomeInterface $e)` never caught -- and fixing it moved the
corpus by **one file**.

That is not a small defect measured accurately; it is a real defect the corpus
can barely reach. 38 files use `instanceof` at all, 15 of those are
`unsupported` for other reasons, and most of the rest test against a class,
which already worked. The corpus finds a defect only through files that clear
**every other gap first**, so a fix deep in a common operation can look like
nothing.

Two readings to avoid, in both directions:

- `+1` does not mean "small fix". Say what the defect was and how a program
  would hit it, not only what the count did.
- `+1` does not mean "no fix" either. A wrong answer removed is worth landing
  even when nothing in the corpus was positioned to show it.

When the movement does not match the size of the change, **say so and say why**
-- and prefer a fixture that exercises the shape directly, since that is the
only evidence there is.

## Rank a gap by what it blocks ALONE, not by how often it appears

The census counts files *containing* a gap. What moves the count is files whose
**only** gap it is, and the two rankings differ enough to pick the wrong target:

	library function exit                     appears 29   only gap 17
	call.static with a dynamic method                 17            10
	ZEND_FETCH_LIST_W                                 12             0
	ZEND_INIT_PARENT_PROPERTY_HOOK_CALL               25             0

`-census-dump` writes one line per file with its whole gap set, tab-separated,
so the sole-gap count is one awk:

	awk -F'\t' -v g="library function exit" '$2 == g' dump.txt | wc -l

A gap with a sole-gap count of zero is still worth fixing -- it is on the path
of everything behind it -- but it will report as a flat tick, and that should be
known before the corpus runs rather than after.

## A private exit code a program can forge is not a signal

`exit 3` meant "the generated program hit a capability gap". The moment PHP's
`exit` started working, `exit(3)` in a test was graded `unsupported`.

The fix is to require the code **and** a stderr marker only the generated
program writes (`php2go: unsupported:`, which is `UnsupportedError`'s own
prefix). Any out-of-band channel shared with the program under test needs
something the program cannot produce by accident.

## `errored` is three different things

The bucket has caught all three now, and they want different responses:

- **A Go build failure.** `cannot use &p_x_t11 as *php.Value` -- the emitter
  produced something that is not a program. Always a compiler bug, usually a
  missing type test.
- **A crash.** The emitter panicked, which takes the corpus run down with it.
- **A timeout.** The emitted program is a valid Go program that does not
  terminate. This is the one that reads as harmless -- 30 seconds of nothing --
  and it is the one where the compiler produced a program with wrong *control
  flow*, which is worse than a wrong value.

`-keep` writes the emitted source for a `failed` file and now for a timed-out
one too. It still writes nothing for a build failure or a crash, where the error
message names the line and that is usually enough.

## `-n` is a file limit, not a parallelism setting

`php2gotest -n 8` means *"stop after this many files"*. Passing it to a
segmented corpus run silently graded the first 8 files of each chunk and
reported `files 96` for a 4,986-file corpus. **The per-chunk completeness check
does not catch this**, because a chunk that stops early still writes a summary
line -- that check only finds a chunk that writes none. Checking the total
against the known corpus size is what caught it, so do both.

## A passing set is the complement, not a grep

`-v` prints a line for every **non-passing** file and nothing for a passing one,
so `grep '^passed '` over the output yields nothing and a passing-set diff of
`+0 / -0` -- which looks like "nothing moved" rather than like a broken
measurement. Build the set per chunk as the chunk's input list minus the paths
it reported:

    grep -hE '^(failed|unsupported|errored|noreference) ' x3$q.txt | awk '{print $2}' | sort -u > np.$q
    sort -u q$q > all.$q
    comm -23 all.$q np.$q >> passing.txt

Then check the result's line count against the run's own `passed` total. If the
two disagree the diff is not trustworthy.

## Re-cluster on the same message before claiming a family

Fixing the first call site that produces a diagnostic is not fixing the
diagnostic. `Cannot add element to the array as the next element is already
occupied` had **four** producers -- `writeDim`, `ArrayAppend`, `ArrayUnpack`
and `BindDim` -- and the first fix looked complete because the probe that
motivated it only exercised one. Grep the run's failure reasons for the message
again after the fix; what is left is the rest of the family.

## An effect column is a claim about the engine

`ArrayAppend` was catalogued `Effects: Pure, Verified: true` and could throw.
`Verified` had been earned by its *value* behaviour; nobody had asked the engine
about its effects. The emitter believes that column -- `canDiagnose` reads it to
decide whether a user error handler could have run -- so a wrong entry is a
wrong program, not a stale note. A new or changed effect entry owes a probe like
any other behavioural claim.

## A mechanism that runs at the wrong moment gives a plausible answer

`materialise` built exception objects at the *handler*, so every error raised by
`runtime/php` inside a magic method or a property hook carried `#0 {main}` and
the caller's line. That is a real stack trace and a real line number; it is
simply not this error's. Nothing about it looks like a bug in a diff until it is
compared against the engine.

Where a value is *captured* is part of its meaning. When a fix is "record X",
ask which frame is on the stack at the moment it records, not only whether the
recording happens.

## Rank failures by shape, not only by message

Clustering on the diagnostic text finds families of three. Clustering on the
*shape* of the diff -- `object(X)#a` against `object(X)#b`, `got ""` where
something was expected, a first-wrong-line of 1 -- finds families of thirty, and
those split into several causes worth chasing separately. Both are worth doing;
the shape pass is the one that finds a lever.

## Two bugs cancelling is not one behaviour working

`gh10168/assign_static_prop_ref` passed because php2go printed `NULL` for a
top-level reference (a var_dump bug) where the engine prints `NULL` through
destructor timing. Fixing var_dump made it fail -- correctly, into the
destruction-order family it always belonged to.

So a **loss** in the passing-set diff is not automatically a regression. Read
what the file now prints and why the old output matched: if the fix made the
program more right and the test still fails, the pass was never evidence.

## A guard the read path has, the write paths need too

Property hooks re-enter: inside a hook, `$this->p` **is** the raw backing store.
The read path had that guard from the start. Adding refusals to the write paths
without it turned two working oss-fuzz regression tests into fatals, and the
corpus caught it as a `-3` in the passing-set diff.

When adding a rule about a property, ask what it does while a hook for that same
property is running. It is now a named predicate (`insideHook`) rather than an
inline map lookup, so the next rule has somewhere obvious to ask.

## `runtime/interp` knows things `internal/gogen` has not been told

Two gaps this session were the same shape: `php.Unshare` is called at the
interpreter's unset points and never by the emitter, and `__unset` was in the
interpreter's magic table and not in `hasMagic`. Both looked like missing
features and were really missing *transfers*.

When a compiler-side behaviour is wrong, grep `runtime/interp` for the same
concept before designing anything. The interpreter has 4,712 tests behind it and
is often already right.

## The second mechanism of a shape has the same questions as the first

Property hooks reached as storage took four probes to pin down: does the
accessor run before the refusal, what does the by-reference form do, what does
an object value do, and what happens while the accessor is already running.
`__get` and `offsetGet` reached the same way needed the same four answers, and
asking them up front made the second and third implementations quick.

When a rule turns out to have parts, write the parts down as questions. The next
mechanism of that shape will have them too.

## When the failure side goes long-tailed, rank the unsupported side

Once no failure-message cluster is above five files, message clustering is spent.
Switch to ranking `unsupported` reasons by what they block **alone**
(`-census-dump`, then `awk -F'\t' -v g="X" '$2 ~ g' | wc -l`). Below the library
surface there is usually a short list of small compiler defects.

Two kinds turn up there and they are worth telling apart:

- A **non-choice refused as though it were a choice.** `frameless_available`
  guards a fast path whose false arm is the ordinary call, so emitting `false`
  loses nothing -- the refusal bought no fidelity and cost every body containing
  one.
- A **real choice nobody had written down.** `assertions_enabled` genuinely
  cannot read the harvesting build's ini, and that is an argument against
  reading it, not against emitting the language default.

Both had sat as gaps for the same reason: the comment explaining the difficulty
was correct and no one had asked what the answer was.

## `noreference` is the harness failing to grade, not php2go failing

A fixture whose PHP is itself invalid -- redeclaring an internal function like
`key()` is a fatal compile error -- comes back `noreference`. That is the oracle
saying it could not produce an expected output, so read it as "fix the fixture",
never as a php2go result.

## A confident comment is still just a claim

`writeDim` carried "An empty string is not a string for this purpose: it
autovivifies like null does". Measured, all three empty-string writes take the
ordinary string path in PHP 8.4. The comment was specific, plausible, and load-
bearing, and nobody had asked the engine.

The founding rule reaches the tree's own prose, not only new work: when a
comment states a behaviour and a case near it is failing, probe the comment.

## Probe the whole row, not the value in front of you

The failing case was `false[] = 42`. Probing `false`, `null`, `""`, `0`, `true`,
`1.5` and `"ab"` in one go cost one `php2eval` call, and it is what turned up the
empty-string error as well -- which was a *different* bug in the same function
that no failing test had pointed at yet.

An `if` with a `default` arm is a row. Ask the engine for all of it.

## Write the probe wider than the feature

A probe for `get_called_class()` included `$b->who()` on a class whose `who` was
static, and that line turned up a bug worth seventeen corpus files: a static
method reached through an instance was `Call to undefined method`. Nothing in
the census pointed at it -- those files were `failed`, not `unsupported`, and
spread across `lsb_*`, `indirect_*`, `traits/` and several `bug*` names with no
shared message.

A probe run against the engine is a differential test for everything it touches,
and the extra lines cost one `php2eval` call. Write them wider than the feature.

## An empty name in a refusal means the wrong shape, not a missing case

`terminator ` and `wants a  argument the IR does not carry` both had nothing
after them, and both meant a construct had been built in the wrong shape -- a
terminator emitted as an instruction, a place built by hand instead of through
`l.place`. Neither was a missing case in the emitter.

## A specialization is a claim about the subject, not about the walk

The type pass proved "this foreach subject is an array" and picked
`IteratorNextArray`, which steps a snapshot index. A **by-reference** cursor over
an array satisfies that proof and holds a bucket position instead, so the fast
path advanced nothing and the loop hung.

When adding a second representation behind an existing proof, check every
specialized path that proof unlocks. The proof did not become false; it stopped
being sufficient.

## Write the fixture before believing the comment you just wrote

Two sentences in the foreach-by-reference fixture asserted behaviour I had not
probed -- that a by-reference loop over a generator degrades to by-value, and
that a plain object is walked live. The engine refuses the first with an
Exception and writes through the properties in the second. The fixture failed on
its own prose before it failed on the code.

## A send mode is a contract with every receiver, not just the one you tested

Changing `ZEND_SEND_VAR_EX` from `byval` to `preferref` fixed six files and
broke seventeen. The mode was right about the engine; what it missed is that the
engine emits that opcode for **every** unresolved callee -- a method call, a
plain `get_class($a)` -- so the argument reached `callBuiltin`, which has no
unboxing step at all.

Before changing how arguments are passed, enumerate the receiving ends: user
functions, closures, methods, builtins, the class-introspection group. A change
at the send site is a change to all of them.

## Ask the engine for the table, not your memory

The by-reference arginfo for PHP's internal functions is 57 entries, and no
amount of recall gets it right. `get_defined_functions()["internal"]` through
Reflection, checking `isPassedByReference()`, produces it in one probe -- and
the same trick works for any "which builtins do X" question.

Harvest the whole table even where php2go implements only part of the surface.
A name outside the surface is refused before the table is consulted, so the
extra entries cost nothing and are already correct the day that name lands.

## Classify the consumers, then change the producer

Three attempts at one send-mode change: -17, -3, then +3/-1. The difference was
not insight, it was a command --

    for fn in <the ten helpers>; do count `args[i]` reads in its body; done

-- which said "three of ten read their arguments directly" in one shot, where
the corpus had been handing them over one failure per tick.

When a change alters what a producer hands over, enumerate the consumers
mechanically before making it. A grep over the emitter is cheaper than a corpus
run, and far cheaper than a revert.

## Some divergences are worth taking, if you can state them exactly

`f($undef)` warns and leaves the variable undefined for a by-value parameter,
and is silent and defines it for a by-reference one. The engine can tell because
it settles the send after resolving the callee; php2go builds its argument list
first, so one send cannot do both.

Taking the silent branch loses one corpus file and fixes a whole class of
discarded writes. That is a fair trade **because the lost case is stated
precisely, has a named witness, and has a recorded fix**. A divergence you can
only describe as "some edge cases" is not.

## A field a later pass rewrites cannot be read by an earlier one

`Regions[r].Finally` starts as the finally's original block and is replaced by
`unwindPaths` with the exceptional copy. `catchBodies` runs first, so reading it
there bakes in an id that will not be emitted -- reported as `jump to a block
that is not emitted`.

The fix is the shape that pass already uses for its other cross-pass work:
record the *intent* (this edge leaves through region r) and resolve it once the
producing pass has run. When a value is a forward reference, store the reference
rather than the value.

## A probe that finds nothing becomes a fixture

Two of three wide probes this tick came back clean -- forty-odd comparison and
conversion cases, and the whole statics/constants/enums/clone area. Thrown away,
that is an hour spent proving something already true.

Copy a clean probe into `cmd/php2gotest/testdata/` with a sentence saying what
it pins. It cost nothing extra, the engine already graded it, and it is a
regression guard over exactly the semantics nobody was going to write a fixture
for on purpose.

## Report a tick that moved nothing

The loop-owned generator unwind is measured, engine-checked and correct, and the
corpus did not move by one file -- no test's *only* gap was that. Saying so is
the point: a capability that the metric cannot see is still worth having, and
pretending otherwise in either direction is what the honesty rule is about.

Quote the unchanged numbers rather than omitting them.

## A refusal names the operand it reached, not the one that is missing

`prop.ref wants a lit argument the IR does not carry` was a missing **receiver**:
the place carried one operand where three were declared, so the shape check
complained about the argument *after* the gap. Same for `dim.ref wants a
argument` with an empty shape name.

When an arity complaint names argument N, look at N-1 and at what should have
produced it. The emitter can only report where it ran out.

## A refusal is only honest when the program really cannot run

`unsupported` is for a gap in php2go. It is **not** for a construct PHP itself
rejects at run time: reading `$this` outside a method is a catchable Error, and
declining to compile the body turned a program the engine runs into one php2go
could not. Four corpus files were waiting on nothing but that.

Before adding a refusal, ask the engine what the construct does. If the answer
is a diagnostic, emit the diagnostic.

## Extract the rule on the second repeat, not the third

The compound-assignment arm missed the same class of rule three times. The
second instance is the one that tells you the rule has sites; by the third the
cost is already paid twice. `receiverOperand` exists now, and its comment says
it has been forgotten twice -- which is what stops someone inlining it again.

## A second path to an operator needs the operator's whole contract

`constOp` called `php.Sub` directly where the ordinary emitter wraps every
arithmetic operator in `AcceptsArithmetic` -- so `const T = [1,2] - [0];`
answered `int(0)` instead of raising. The catalogue records the guard beside the
operator for exactly this reason; the constant-expression renderer was reading
only the `Go:` field.

When a second consumer reaches an operation, read every column of its catalogue
entry, not just the function name. The guard, the effects and the failure class
are part of what the operation *is*.

## Probe where a value can be absent, not only where it can be refused

Two ticks running, the bug was a **missing arm** rather than a refusal: an
operator called without its guard, and a `switch` with no case for the deferred
form of a default. Neither produced a gap, an error, or a census entry -- one
gave a wrong number and the other gave `null`.

The census only lists what php2go declined to compile. A wide probe over an area
where several inputs reach one renderer is the way to find what it compiled
wrongly, and the areas worth probing are the ones with a *second path* to
semantics the ordinary emitter already implements.

## When adding the missing arm, ask when it should run

The static-property arm was one line, and one line was wrong: `initStatics`
runs before `initConstants`, and PHP evaluates such an initializer on first use
anyway. The eager version would have passed the fixture that was about to be
written for it -- the engine is what said otherwise.

## Vary the asking scope, not just the asked question

A probe over `property_exists` asked every shape of *subject* -- child instance,
parent instance, class name, dynamic property -- and every one from **global
scope**. The answer turns out to depend on the caller: inside the declaring
class a parent's private is visible, outside it is not. The probe passed the
wrong fix; a corpus file caught it.

For anything visibility-related, ask from outside, from the declaring class, from
a subclass, and from a static method of each. That is four lines, and it is the
axis a probe written from the outside will always miss.

## Test every kind of member, not one kind thoroughly

Instance properties have been visibility-checked since early on. Static
properties were not checked **at all**, and no test noticed, because the tests
that care about visibility all use instance properties.

A probe over an access rule should cross two axes: the asking scope, and the
kind of member -- instance property, static property, method, static method,
class constant. The rule is one rule; the implementations are five.

## Write the fixture for the whole cross before fixing anything in it

The private-constant fix broke protected constants in the same position, and the
fixture caught it inside one tick -- because it had been written to cover every
visibility against every asking scope, not just the case being fixed.

A fixture built around one bug confirms the bug is gone. A fixture built around
the *rule* catches the next one you introduce while removing it.

## `php2gotest` takes paths, not a list file -- and reports `files 0` as success

There is no `@file` argument form. `php2gotest -v @list.txt` names a path that
does not exist, and the tool answers with

    files 0
      passed           0  (0.0%)
      ...
      errored          0  (0.0%)

-- a complete, well-formed summary block with a zero in every bucket, exit status
0. Three parallel waves of four chunks each ran to completion against nothing
before the total was checked. Use `php2gotest -v $(cat chunk)`.

The general rule this is the second instance of: **a summary block is not
evidence that anything was graded.** Read the `files` count first, per chunk, and
against the corpus size -- the same check that caught `-n 8` silently limiting
each chunk to eight files.

## The same syntax can ask different questions, and the engine answers each

`$s[$k]`, `$s[$k] ?? "d"` and `isset($s[$k])` are three read modes of one
expression, and for a string subject the engine uses a *different key conversion
rule in each*: a float key deprecates in isset and warns in the loud read; a
`"1x"` key throws in isset's answer (false), warns in `??`, warns in the read; a
`"1.5"` key throws loudly and misses silently.

So `??` is not the loud read with diagnostics suppressed, and `isset` is not
`??` reduced to a bool. When probing an access rule, run **every mode** of it --
read, silent read, isset, write -- across the same key set. Three of the four
rules here were wrong, and one probe of the loud read alone would have found one.

## A probe built around a feature tests only what the feature touches

The `__call` work probed classes whose methods **do not exist** -- that is what
`__call` is for -- so nothing in the probe ever consulted the ordinary
method-dispatch table. The change broke that table, and both the probe and the
fixture built from it passed. Fifty-seven corpus files did not.

When a change touches a shared path, the fixture has to exercise the path's
**other** users, not only the case that motivated it. Here: one call to a method
that actually exists.

## A rename that must reach every use is not a find-and-replace

Three emitter lines produced the same `switch name {` at three nesting depths.
Replacing the exact text caught two and missed the third, and the substitution
count printed 6 -- read as success without anyone having decided beforehand what
the expected number was.

If a count is printed to confirm an edit, predict it first. A count that is only
read afterwards confirms whatever happened.

## `TestDNSQueriesMatchEngine` makes a live DNS query and flakes

`internal/bundlebuild`'s DNS test resolves `localhost` for real. On a loaded or
network-restricted box it fails with

    bundle output = "\nWarning: DNS Query failed in /app/main.php on line 13\nbool(true)..."
    engine output = "bool(true)\nbool(true)..."

and passes on a re-run. Seen once on 2026-09-11 during an otherwise clean gate
on changes that touch neither the bundler nor the DNS surface. Before treating a
failure there as yours, re-run that test alone -- and if it passes, say so
rather than quietly re-running the whole suite until it is green.

## A fixture that is not about object identity should not print objects

`var_dump(new B)` prints `object(B)#2`, and the handle number is the one gap
php2go is guaranteed to differ on — the engine reuses the handle of a temporary
that died and php2go frees nothing. A fixture written to check autoload ordering
failed on the handle and not on the ordering.

Print `get_class($o)` unless the test is about identity.

## Screen candidate failures against the handle-number gap

Two ticks running produced correct work and no measured movement: both areas
ended at `object(X)#2` versus `#3`, the handle a dead temporary would have
freed. php2go frees nothing, so any file whose expected output dumps an object
may dead-end there however much else is fixed.

Before picking a cluster, drop every failing file whose *expected* output
contains `object(...)#` or whose source defines `__destruct`. On the 2026-09-11
corpus that takes 642 failures down to 464 that can actually reach a pass, and
the clusters inside that set are the ones worth ranking.

### A literal is a route text takes into the program

Some PHP text reaches the emitted program without passing through any renderer.
`__FILE__`, `__DIR__` and `__METHOD__` in a closure are folded into string
literals by the Zend compiler; the key of an array literal is emitted straight
from the parsed literal and never sees `operand()`. When a fact has to be
reconciled between the harvest and the emitted program -- the source path is the
one that keeps coming up -- the fix has to cover **every route**, and the routes
are not all function calls you can grep for. A fix that covers three of four
routes does not merely under-deliver: it makes the spellings *disagree with each
other*, which is worse than leaving them all wrong. `gh11320_2` failed that way
and the corpus caught it.

The fixture for a reconciliation like this should print none of the reconciled
text. Assert only that the spellings agree -- `$e->getFile() === __FILE__` --
so the test says what it means and does not have to be updated when the path
changes.

### Diff the passing set by path, not by line

A `-v` run prints `<status>\t<path>`, so diffing two runs' non-passing lines
compares **status plus path**. A file that moves from `unsupported` to `failed`
then shows up in *both* halves of the diff -- once as "newly passing", once as
"newly failing" -- and neither is true. Strip to the path (`awk '{print $NF}'`)
before `comm`, and the two halves mean what they say: left is files that started
passing, right is files that stopped. The right-hand side is the only one that
can contain a regression.

### A generated table failing is evidence, not an obstacle

`TestGeneratedSurfaceIsCurrent` failed during a pure relocation of library
functions between packages, and `go generate` produced a diff turning
implemented functions from `native` to `pending`. Regenerating and moving on
would have committed a table that said php2go had *lost* those functions.

The failure was the generator's: it reads `RegisterBuiltin("name", ...)` as a
string literal and could not see a table registered by ranging over a map. The
test asks whether the generated view still matches reality, and during a move
the answer it should give is **no diff at all** -- a pure relocation that
changes a generated artifact has either not been pure or not been seen
correctly. Check which before regenerating.

### A pure-looking helper can still encode its caller's convention

`callerStack` is five lines that drop the top stack frame. Nothing in it names
the interpreter, and it passed every test in the interpreter after being moved
to the shared package -- because the interpreter is where its assumption holds.
It is really an assertion that *somebody pushed a frame for this builtin*, and
compiled code does not.

So when moving a helper between the two consumers, the question is not whether
its body is pure. It is **what it assumes about the caller that is not in its
signature**, and the only way to find out is to run it from the other consumer.
A probe compiled and executed is what caught this one; the interpreter's own
4,986-file corpus could not have.

### "Internal" is a property of the implementation, not of the name

php2go implements some engine functions in PHP -- the userland catalog -- and
those bodies are written to see themselves in a backtrace exactly where the
engine's C frame would be. So a rule keyed on "is this an engine function" and
applied before the userland branch marks a PHP frame as internal, and anything
counting frames from the top is then off by one.

The symptom was two layers away from the cause: an object's private and
protected properties disappeared from `http_build_query`'s output, because that
function reads `debug_backtrace()[1]['class']` to learn its caller's scope.
`TestEmbeddedProjectMatchesEngine` is the test that sees this class of thing --
it runs the whole userland library against the engine -- and it is worth running
after any change to frames, diagnostics, or dispatch order, not only after
changes to the bundle.

### Two corpus files are coin-flips, by their own construction

`Zend/tests/attributes/deprecated/message_004.phpt` and
`.../class_constants/value_unknown_at_compile_time_001.phpt` both call
`random_int(1, 2)` in the test body and print the result. The differential
harness runs the engine and the compiled program as **two processes**, so the
two draws are independent and the files pass about half the time each.

They will appear in a passing-set diff in either direction and mean nothing.
Check the source before treating either as a regression -- and when a diff shows
only these two, the tick moved nothing backwards.

### An emitted program is its own Go module

`php2gotest` writes `module php2gotest` with a `replace` back to the repo. So
emitted code can import `github.com/moriyoshi/php2go/runtime/...` and **cannot**
import anything under `internal/` -- Go's internal rule is path-based and the
artifact is not under the repo's module path.

That is why the shared library surface lives in `runtime/stdlib` and not in
`internal/`. When deciding where something the artifact needs should live, the
question is not layering taste: it is whether a different module can import it.

### The differential suite only tests what it installs

`runWithSources` builds an interpreter per case. Until 2026-09-12 it did not
install the userland library, so any function whose body is PHP was simply
absent: the harness reported `not yet supported: needs call to ucfirst` and
counted the case as skipped.

That is an honest bucket and a trap. The skip count is printed as
"N awaiting opcode support", which reads like an opcode gap and was in fact a
**missing library**. Moving a function from Go to PHP therefore removed it from
verification at the exact moment it started needing verification. The library is
harvested once per test binary behind a `sync.Once` -- 10k lines through the
engine is too slow to repeat across 1,108 cases.

The general rule: when a skip count moves, read *why* each case skipped before
believing the number. A skip that means "the runtime has not implemented this
opcode" and a skip that means "the test harness did not load the library" look
identical in the total.

### Two generated tables describe the library, and they answer different questions

`internal/userland/surface.tsv` says **where** each PHP function is implemented
-- `native`, `userland`, `unavailable`. `internal/userland/shape.tsv` says what
the `PHP2Go\Internal\*` helper layer underneath looks like: how many call
sites each helper has, how many distinct operation names it is switched on, and
whether compiled code can reach it.

Regenerate both with `go generate ./internal/userland`, and read a diff in them
as review material rather than noise:

- `surface.tsv`: a row flipping `native` -> `userland` is a conversion. A pure
  relocation of Go between packages should produce **no diff at all**; if it
  does, either the move was not pure or the generator cannot see the new home.
- `shape.tsv`: a selector count rising above 1 means an operation was added by
  widening a helper's string switch instead of writing a function. A new
  `reachable=no` row means another userland function just became impossible to
  emit.

### Only `internal/bundlebuild` runs the userland library end to end

`go test ./runtime/...` exercises the interpreter, and since 2026-09-12 the
differential suite installs the userland library too. Neither notices a helper
whose **name** stopped resolving: the differential cases reach the library
through public functions, and a missing `PHP2Go\Internal\*` entry only shows
when a PHP body actually calls it.

`internal/bundlebuild` is the suite that does. Splitting the stream
multiplexers removed a stub from `builtins.go` and with it the registration that
made the dispatcher reachable; every other package passed, and
`TestStreamSocketPairUserlandMatchesEngine` failed with
`Call to undefined function PHP2Go\Internal\stream_socket_pair()`. Run it
after any change to the internal-helper layer, and read a fatal about an
undefined `PHP2Go\Internal\*` function as a **missing registration**, not a
missing implementation.

### The census truncates, and `-top 0` is how you see the whole answer

`php2gotest -census` prints the 30 gaps that block the most files. Ranking a
corpus that is right. Over a handful of files it is actively misleading: every
count is 1, so the cut falls alphabetically and the list you read is the first
30 gap names, not the important ones. Pass `-top 0`.

The same run also taught a second thing about reading that list. A name in it is
not automatically something to go and implement: when the files under census are
a **library**, some of the names are functions that library defines itself, and
they resolve as soon as it is emitted as one unit. Check the disposition in
`internal/userland/surface.tsv` before treating a census row as work -- 14 of 51
were already done.

### A map entry does not end where its braces balance

Moving builtins out of the interpreter's `map[string]builtinFunc` literals by
script is tempting and has gone wrong three times the same way. Counting braces
forward from

	"explode": func(_ *php.Ctx, args []php.Value) (php.Value, error) {

balances at the **parameter list**, not the body: the cut takes one line, the
body is orphaned where it came from, and the file still parses far enough that
the error surfaces somewhere unrelated.

gofmt guarantees an entry ends at a line that is exactly `\t},`. Match that.
The same applies to a top-level function, which ends at a line that is exactly
`}`. And run `gofmt -e` on **both** files after any such extraction -- the
source and the destination -- because the damage is as likely to be in the one
you were cutting from.

### Split the big corpus chunks; four whole ones no longer fit

`php2gotest` is serial within one invocation, so a chunk's wall time is its file
count -- and since the emitted programs began carrying the builtin signature
table they are larger and slower to compile. `q01` (551 files) and `q02` (504)
now exceed the ten-minute tool cap even with four chunks in parallel.

`split -n l/2` them and concatenate the `-v` outputs: the tally sums every
`files`/`passed` line, so several summary blocks add up correctly, and the
passing-set diff reads the per-file lines either way. A chunk that times out
leaves a file with **no** summary block, which is how to spot one -- `grep -c
'^files'` per output before trusting a total.

### Run four corpus chunks at a time, not six

`php2gotest` is serial within one invocation, so a chunk's wall time is its file
count. `q01` is 551 files and is always the long pole. Four chunks in parallel
fit inside the ten-minute tool cap; six do not, and the failure looks like a
hang in the chunk that was still running when the shell was killed.

When `q01` alone is too slow, `split -n l/4` it and concatenate the four `-v`
outputs into one file: the tally sums every `files`/`passed` line, so four
summary blocks add up correctly, and the passing-set diff reads the per-file
lines either way.

### A fixture full of lowercase names proves nothing about case

Adding builtin argument verification broke seven passing corpus files and no
fixture, because PHP method names are case-insensitive and every method in the
fixtures was already lowercase. `[$this, "EH"]` looked up a table keyed `"eh"`
and reported that the class had no such method -- and since a callback argument
is checked *before* the call, a working `set_exception_handler` became a fatal.

When a lookup folds case, the fixture has to contain a name that is not already
folded. The same applies to class names, function names and property names,
which fold differently from each other.

### Split Go with go/parser, and never rename inside a string literal

Two rules from moving 3,700 lines of stream code between packages.

**Brace counting cannot find a declaration boundary.** It fails on a brace
inside a string literal and on a multi-line signature, and when it fails it
does not error -- it truncates, leaving the rest of the body orphaned in the
file it came from. Three attempts failed that way. A short tool over
`go/parser` is correct the first time. When it writes declarations out, separate
them with a sentinel comment rather than a blank line: a struct has blank lines
inside it.

**A rename is about identifiers; a string literal is data.** Rewriting `stream`
to `Stream` across the tree also rewrote `"Argument #1 ($stream)"`, `"supplied
resource is not a valid stream resource"` and the `"stream"` that
`get_resource_type` returns. Skip quoted text in the substitution. Then check
each PHP-visible string against the engine rather than assuming: `"Stream filter
failed"` and `"Stream is not writable"` really are capitalised, so
lower-casing them "back" broke two more tests than the original mistake did.

### `php2eval -raw` is the engine only

`-raw` implies `-engine`. It prints the engine's output byte for byte and never
runs the interpreter, which is exactly right for *asking the engine what PHP
does* and useless for "do we agree with it".

Drop `-raw` for the differential form: it prints `--- ENGINE ---` and
`--- INTERPRETER ---` and ends with `--- MATCH ---` or `--- MISMATCH ---`. Note
that the two arms report different **file names** -- the engine says
`php2go-eval` and the interpreter a temp path -- so a diagnostic carrying a path
mismatches for that reason alone; read the wording, not the verdict, in that
case.

Several probes this session were read as agreement when they were engine-only
runs. The habit that saves it is the one the project already has: add a
differential case, and let the suite answer.

### The engine arm runs one output-buffer level deep

`php2go_eval` captures the engine's output with `php_output_start_default()`,
because an in-process capture cannot redirect a file descriptor without
interleaving with Go's own writes. That buffer is a real `ob_*` level and the
script can see it:

| under `php2eval` | under the CLI |
|---|---|
| `ob_get_level()` at top level is **1** | 0 |
| `ob_get_contents()` with no buffer of your own answers `""` | `false` |
| `ob_get_status()` lists one `default output handler` | an empty array |
| ending more buffers than you opened tears down the capture, and the run appears to print nothing | the notice PHP prints |

So **a differential case must not observe the buffer level.** Read what a
handler produced, not how many handlers there are; end exactly what you opened.
Eight cases written the other way looked like eight interpreter bugs.

The buffer is invisible to anything that only writes, which is why the corpus
has never noticed -- no file in it reads `ob_get_level`, `ob_get_status` or
`ob_list_handlers`. It is still a fidelity gap; see TODO.md.

### A backtrace frame carries two things the oracle cannot make agree

`file` is the harness's own path -- the engine arm reports `php2go-eval` and the
interpreter a temp file -- and an object's handle number is the harness's own
count, which starts wherever the engine's store happens to be. So a differential
case over `debug_backtrace` or `getTrace` prints the frames **key by key**, skips
`file`, and renders `object` as its class name rather than dumping it. Four
cases written the obvious way failed on nothing but those two keys while every
other byte matched.

### `exit` and the shutdown functions

`exit` does not bail the engine arm, but the shutdown functions PHP runs after it
are not in the capture, while the interpreter runs them -- so a
`register_shutdown_function` after an `exit` mismatches for the harness's reason,
not the runtime's. A request-ending **fatal** (`trigger_error(E_USER_ERROR)`,
an uncaught error under some shapes) is worse: `zend_eval_string` bails out and
the oracle produces no reference at all, so the file grades `noreference`.
Nothing downstream of such a fatal can be measured by this harness.

### Object handles accumulate across differential cases

The engine arm runs every case in this file in **one process**, so object
handles do not start at 1 -- they start wherever the previous case left off. A
case that prints `spl_object_id` or a `var_dump` handle must therefore compare
against a baseline it takes itself, and must hold that baseline **in a
variable**: a temporary is freed immediately, and whether PHP reuses its handle
is a separate question that will make the case fail for the wrong reason.

`php2gotest` does not have this problem -- each file is its own process -- so a
compiled fixture can print absolute handle numbers.

### A differential case must put back what it changed

The engine arm shares one process, so anything a case writes to **request
state** is still there for the case that runs next. An `ini_set` left standing
turned up as a later syslog case reading `syslog.filter` as `"raw"`, and the
mismatch was reported against *that* case, not the one that caused it.

The interpreter arm gets a fresh Unit per case, so the leak is one-sided and
looks like an interpreter bug. Restore the setting at the end of the case that
changes it.

### `grep -a` over a corpus run's output

A `.phpt` can expect output containing a NUL, so a `-v` chunk file that quotes
one is a **binary file** as far as grep is concerned: it prints `Binary file ...
matches` and none of the lines. The passing-set diff then counts every file in
that chunk as passing. It surfaced as `gained: 381` on a tick that gained two,
and the guard that caught it is the one that should always be applied -- the
passing set's size must equal the run's own `passed` total.
