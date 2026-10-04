# Quality Gate

Before reporting a change complete, run the checks relevant to the files
changed.

## A comment asserting a safety property is not the property

2026-08-29, `internal/gogen`. An operand the emitter could not render was
emitted as:

```go
return "/* UNRENDERABLE: " + s + " */ php.Undef()"
```

under a comment stating this was *deliberately invalid Go, so the build fails
loudly rather than the program running with a hole in it*.

It is perfectly valid Go — a block comment followed by an expression — so the
program built and ran with exactly the hole the comment said it could not have.
`$a = [1, 2, 3]` left `$a` unset and the `foreach` over it died on null.

The failure mode is this project's recurring one: **a conclusion survived and
its stated reason was never checked.** Here the reason was a claim about the
emitted language, written as a comment, in the one file whose job is to be
correct about that language. Nothing tested it, and its confident presence made
it *less* likely anyone would.

As a rule rather than an anecdote:

- **A refusal must be a returned error, never a rendered placeholder.** Where a
  path has no error return, park the failure and check it. Do not encode a
  refusal in the output and hope a later stage rejects it.
- **If a comment claims the output will be rejected downstream, there must be a
  test that the downstream stage rejects it.** Otherwise it is untested prose
  guarding a silent hole.
- Every number measured before such a fix is suspect **in the flattering
  direction**. Re-measure and report the movement, including when there is none.

## Go gate

```sh
eval "$(hack/cgoenv.sh)" && hack/gate.sh
```

Run the script rather than the four commands by hand. **The hand-typed form is
where both sessions independently wrote the same bug** (2026-08-30): a banner
printed on the line *after* a pipeline, so it said the same thing whether or not
the pipeline found anything.

```sh
go vet ./... 2>&1 | grep -v '^#' | head     # exit status is head's, and is discarded
go test ./... && echo "GATE GREEN"          # the banner comes from a different command
```

That printed `GATE GREEN` with two `copylocks` findings on screen. It stayed
hidden because `go test` re-runs a *subset* of vet, so the common checks looked
gated and everything outside the subset failed silently.

`hack/gate.sh` computes its verdict from the accumulated status of every check,
so the message cannot be printed without having run them. It also **anchors to
the repo root from its own location**, never from the working directory.

That second part was a second bug of the same species, found by the runtime
session after the rewrite. `gofmt -l .` and `./...` are both cwd-relative, so
running the gate from `internal/` reported `GATE GREEN` with an unformatted file
in `runtime/php/` — and, worse, with a *failing test* there. The first four
mutation checks missed it because **all four were run from the repo root**,
which is the normal case and therefore the case anyone tests. The working
directory is an input, and it is the one that does not look like an input.
`hack/cgoenv.sh` had already solved it next door.

Mutation-checked in four directions × two working directories, all eight:

| | from the root | from `internal/lower` |
|---|---|---|
| unformatted source | `GATE RED: gofmt` | `GATE RED: gofmt` |
| vet finding `go test` does not re-run | `GATE RED: vet` | `GATE RED: vet` |
| a failing test | `GATE RED: test` | `GATE RED: test` |
| a package that does not compile | `GATE RED: build vet test` | `GATE RED: build vet test` |
| clean tree | `GATE GREEN` | `GATE GREEN` |

### Announce a shared-API change before making it

Changing `Iter.Current`/`Iter.Key` to take a ctx left `runtime/php` non-building
for a few minutes. Twenty-five generated programs failed to build against it, the
compiled harness scored them all `errored`, and the corpus run was void.

**An incomplete warning beats a complete one that arrives after the other session
has measured.** The mistake was deciding to finish the implementation first so the
message would be complete.

### A single `GATE RED` on a shared tree is not a result

Two sessions write this tree, so a check can catch a file between writes or a
package mid-edit. The runtime session saw four REDs in five minutes, every one
of which resolved on a re-run, with their own files clean throughout — my edits
landing mid-write.

That is the gate reporting a **real state**, not a false positive: the tree
genuinely was inconsistent at that instant. It is the same rule the corpus
already has for a shared *box* — *a measurement with compile errors is noise;
read the compile-error count first and re-take it* — applied to a shared *tree*.

The script re-runs on failure rather than leaving that as advice, and reports
three verdicts:

| verdict | exit | meaning |
|---|---|---|
| `GATE GREEN` | 0 | clean |
| `GATE RED: <checks>` | 1 | failed **and reproduced** |
| `GATE UNSTABLE: ... changed mid-run` | 2 | the gate script or the source moved — not a result |
| `GATE FLAKY: ... with the source unchanged` | 3 | **nothing external moved and the verdict flipped — the check is nondeterministic, which is a defect** |

`FLAKY` exists because `UNSTABLE` conflated two populations, and the runtime
session constructed the case that proved it: a test that fails on its first run
and passes on its second reported "shared tree; not a result" when the tree was
not the cause. That is the same bucket problem as `php2gotest`'s `unsupported`
one level in — a gap mixed with a non-result, and **the non-result reading is
the flattering one**. It is not hypothetical: differential cases here can depend
on engine *process* state, which is exactly a check whose verdict depends on
what ran before it.

The split is the script-checksum move one level out. The script's checksum
separates "the gate moved"; an input-set checksum over every `*.go` and
`testdata/` file separates "the source moved" from "nothing moved". A flaky
check and a failing check want different responses, so they get different
verdicts.

All four constructed rather than assumed: a persistent failure, a file deleted
one second into the run, a copy of the script appended to mid-run, and a test
keyed on a marker file so it fails once and then passes.

### `GATE GREEN` means *passed once*, not *passes reliably*

The re-run fires only on **failure**, so a check that fails then passes is caught
and one that **passes then fails is never looked at** — constructed by the
runtime session: a test that passes on its first run and fails on every run after
gives `GATE GREEN`, exit 0.

That `FLAKY` exists makes this worse rather than better, by suggesting flakiness
is covered when it is covered in one direction. The blind direction is, once
again, the flattering one.

Of the four checks **only `test` can be nondeterministic** — gofmt, build and vet
are pure functions of the input set — so closing the asymmetry costs one extra
test run rather than doubling everything:

```sh
GATE_TEST_COUNT=2 hack/gate.sh    # go test -count=2
GATE_TEST_COUNT=3 hack/gate.sh    # a fixed point, not a coincidence
```

Not the default, decided on measurement: `-count` disables the test cache, so it
turns a 2s gate into a 34s one on **every** run. Use it before a claim that
matters. `GATE GREEN` says so in its own output when it did not.

An unstable run says **which thing moved**, because "the tree changed" and "the
gate changed" are different findings and only one is about the code. The script
records its own checksum at start and compares at the end; a RED verdict carries
that checksum too, so two observations can be told apart.

That distinction exists because the runtime session read RED from one directory
and GREEN from two others, concluded the cwd fix had failed — it agreed with
their prior, since they had found that bug — and were stopped only by seeing a
verdict the script had not previously had. **It was being edited while they read
it.** Their three readings reproduced across *space* and not across *time*.

Verified by constructing a genuinely disappearing failure, not by assuming:
an unformatted file removed one second into the run yields `GATE UNSTABLE` and
exit 2, while the same file left in place yields `GATE RED` and exit 1.

**Corollary:** neither session may conclude the other broke something from a
single RED. It needs a re-run, and only a reproducing failure is real.

**The defence is structural, not vigilance.** Both sessions knew this failure
mode — one of us had just finished writing it up in TESTING.md — and typed it
anyway, because it is the shape of the convenient command. Make the success
message a function of the check rather than a line that follows it.

If a Go file or testdata directory was added, update the matching `BUILD.bazel`;
the script does not check that.

## Behavioural validation

- Harvester changes need a differential case that exercises the opcode or
  structure, not just a successful compile. Operand encodings are settled by the
  engine, never by reading the headers.
- Interpreter changes need a differential case whose expected output came from
  the engine.
- Value-model changes belong in `runtime/php` tests and, where behaviour is
  observable from PHP, in a differential case as well.
- Before moving a function into the shared surface, decide which of **three**
  it is. *Moved*: the semantics are the same for both consumers. *Refused*: the
  capability is absent and must be named. **Not-shared**: the answer is a
  property of the **runtime** rather than of PHP -- `gc_collect_cycles` is true
  for the interpreter, which really collects, and a fabrication for compiled
  code, which has no registry. That one stays with the consumer whose model
  makes it true, and the other refuses it.
- **Not-shared is asymmetric, and that is the point.** The two consumers give
  *different answers by design* -- the interpreter collects and answers `1`, a
  compiled program cannot and refuses. It is the one case where the interpreted
  and compiled corpora legitimately diverge on the same test rather than one of
  them being wrong, so a round comparing the two numbers must not read it as a
  defect. **They must also be run
  through the compiled path**, because `runtime/php` now has two consumers and
  the interpreted corpus only exercises one:

  ```sh
  go build -o /tmp/php2eval ./cmd/php2eval
  go run ./cmd/php2gotest -php2eval /tmp/php2eval cmd/php2gotest/testdata
  ```

  Those cases write down **no expected output** -- it comes from the engine at
  grading time -- so they cannot drift into recording what php2go does. Checked
  that they bite rather than assumed: swapping the `""--` special case in
  `php.Decrement` for the ordinary non-numeric-string one turns the corpus red,
  5 passed to 4 passed / 1 failed.
- Request-lifetime behaviour goes in `testdata/lifetime`, because the
  differential oracle cannot see request shutdown.
- IR or proto changes need round-trip and determinism coverage.

Before trusting a passing check, identify how it would fail if the claimed
behaviour were absent. A compile failure after neutralizing a fix is not
behavioural evidence.

## Honest failure reporting

A change must not convert a gap into plausible-but-wrong output to raise the
pass rate.

- The interpreter lacks a capability the linked build has → `UnsupportedError`,
  counted separately by `php2phpt`.
- The program did something wrong → throw the PHP error with the engine's exact
  wording, because the program may catch it and the test usually expects the
  text.

Decide between them from harvested data — is the function in `Manifest.builtins`,
is the class `builtinOnly`, was the method declared by the program — not by
guessing.

## Measurement claims

If the change is claimed as progress, quote before-and-after counts from the
same corpus and the same command, and report `passed`, `failed`, `unsupported`,
and `errored` together. A pass-rate number alone is not evidence.

Quote the denominator with the rate. `compile-error` and `skipped` are excluded
from `attempted`, so a change that moves either bucket moves the denominator and
makes two rates incomparable — 1022/3860 and 2012/4569 are the honest pair, not
26.5% and 44.0%.

## Correct the claim, do not append a correction

2026-09-06. `IR-ASSEMBLY.md` asserted `verify_return`, `assign_op` and
`local.set` as the current IR while sections appended below it explained that
each had been replaced. Every section was true when written; the document was
false.

This is the drifted-lookup-table defect in prose, and it arrives the same way:
**a second statement of one fact, only one of which is maintained.** A design
document that grows a correction per change becomes a changelog, and a reader
has to reconstruct the present from the diffs.

Two rules:

- **Edit the stale sentence.** If a change makes a claim wrong, the claim is
  what to fix; the history goes in the journal, which is *for* history.
- **Keep one inventory.** A document describing a language needs one place that
  says what the language currently contains, ahead of the reasoning. Without it
  nobody can tell whether a section is current or archaeological.

## A golden over a nondeterministic renderer is a coin toss

2026-09-05. `TestGoldenIR` failed once and passed on the next run with the
source unchanged. The cause was not the test: mem2reg allocated block-parameter
value numbers by ranging a **map**, so the same file lowered to two
different-looking IRs depending on Go's per-run map randomisation.

Two things to take from it:

- **Reproducibility is a property of the compiler, not of the test.** An IR that
  differs run to run cannot be diffed, cached, or pinned. Any pass that allocates
  names, or emits in the order of a map, has to sort first.
- **`FLAKY` earned its place.** Under the older two-verdict scheme this would
  have read as `UNSTABLE` -- "the shared tree moved" -- and been dismissed,
  because two agents write this tree and that is usually true. It was not true
  here, and the flattering reading was the wrong one.

The guard for it is explicitly **probabilistic**: re-introducing the bug fails it
on some runs and not others. Say so in the test rather than implying a guarantee,
and run it a few times when the ordering of anything in the pass changes.

## `git checkout-index` is `git checkout`

2026-09-03. The rule is *do not use `git checkout` or `git restore` on a shared
working tree*. I reverted a temporary probe with

```sh
git checkout-index -f cmd/php2gotest/main.go
```

which is the same operation wearing a different name, and it silently discarded
**every unstaged change in that file** -- three edits' worth of working code --
because the tree is staged but has no commits, so the index was a stale
snapshot. Nothing failed at the time. The loss surfaced two commands later as a
measurement that had quietly stopped measuring: the census printed no
specialization line at all, and 400 operations reached the emitter unspecialized.

Two rules, not one:

- **The named-command rule was too narrow.** The thing to avoid is *any* command
  that overwrites a working file from another source -- `checkout`, `restore`,
  `checkout-index`, `stash`, `read-tree -u`, `clean`. Name the behaviour, not the
  three spellings, or the fourth spelling gets used.
- **Revert a temporary edit with the copy you took, never with git.** A probe
  should be undone by restoring the `cp` backup made when it was applied. Git
  knows what was *committed*; on a tree with no commits it does not know what was
  working a minute ago, and it will not say so.

Recovered from the session transcript, which records every edit as the command
that made it -- worth knowing before it is needed.

## Final review

- Inspect `git diff` and `git status`.
- Confirm unrelated user changes were preserved and `third_party/php-src` is
  untouched.
- Confirm no binary, cache, credential, Bazel output, or temporary artifact was
  added to the tracked tree.
- Record durable findings in the journal when they will help future work.

## A refusal gated on a declaration fails open

`internal/gogen` refused a place in receiver position with
`if v.PlaceGap != "" && vtypes[a] == "place"`. The intent was "refuse, and say
why"; the effect was "refuse **only if** someone remembered to write the
message". `prop.set` arrived without one and emitted Go that did not compile,
turning a case that had been honestly `unsupported` into `errored`.

The rule: **a safety check must not be conditional on metadata being present.**
Check the condition unconditionally; let the declaration supply the wording and
default it when absent. The same shape to watch for is a validation loop that
`continue`s on an unknown kind, or a switch whose `default` does nothing.


## Write each file before the next assertion, not after

Scripted edits in this session repeatedly matched on a **gofmt-aligned** literal:
`"sprop.set":  true,` has two spaces after the colon in one map and one in
another, because gofmt aligns map values per contiguous block. A match written
from memory misses, and the failure mode depends on where the write happens:

- Assert, then write, per edit: the mismatch is reported and nothing is lost.
- Several edits to one file, asserted throughout, written once at the end: an
  assertion failing on the *last* edit **discards every earlier edit to that
  file**. This happened to `internal/lower/specialize.go` -- two correct,
  asserted, applied edits vanished because a third match failed, and the symptom
  was an intrinsic that stayed an opcode with no error anywhere.

So: one edit, one assertion, one write. And prefer matching on a **whole line**
compared exactly, or on a structural anchor, over a substring whose interior
whitespace gofmt owns.

The tell for the discarded-edit case is that a change appears to have no effect
at all -- the build passes, the tests pass, and the thing you edited behaves as
before. Check the file, not the diff of what you intended.

