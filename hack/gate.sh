#!/usr/bin/env bash
# The Go gate, as one command whose verdict is computed from the checks.
#
# Written after the same bug appeared in both sessions' hand-typed gates: a
# banner printed on the line *after* a pipeline, so it said the same thing
# whether or not the pipeline found anything. Mine reported GATE GREEN with two
# `copylocks` findings on screen -- `go vet ./... | grep ... | head` discards
# vet's exit status, and `go test` only re-runs a subset of vet, so anything
# outside that subset failed silently.
#
# The defence is structural rather than vigilance: both of us knew the failure
# mode -- one of us had just finished writing it up -- and typed it anyway,
# because it is the shape of the convenient command. So the success message is a
# function of the accumulated status here, and there is no way to print it
# without having run everything.
#
# Usage: eval "$(hack/cgoenv.sh)" && hack/gate.sh [packages...]
set -uo pipefail

# Anchor to the repo root from *this script's* location, never from the working
# directory.
#
# Without it every check silently narrows to whatever subtree the caller happens
# to be in: `gofmt -l .` and `./...` are both cwd-relative, so running the gate
# from `internal/` reported GATE GREEN with an unformatted file in
# `runtime/php/` and, worse, with a *failing test* there. The working directory
# is an input, and it is the one that does not look like an input -- which is
# why four mutation checks missed it: all four were run from the repo root,
# because that is the normal case and therefore the case anyone tests.
#
# Same anchor as hack/cgoenv.sh, which had already solved this next door.
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$root" || exit 1

# This script's own identity, so a reader can tell whether two observations were
# of the same gate.
#
# The runtime session read RED from one directory and GREEN from two others,
# concluded the cwd fix had not worked -- it agreed with their prior, since they
# had found that bug -- and were stopped only by seeing a verdict this script
# had not previously had. It was being *edited* while they read it. Their three
# readings reproduced across *space* and not across *time*, which on a shared
# tree is the axis that matters.
#
# So an unstable run reports which thing moved. "The tree changed" and "the gate
# changed" are different findings and only one of them is about the code.
self_sha() { sha256sum -- "$0" 2>/dev/null | cut -c1-12; }
gate_sha_start=$(self_sha)

# The input set: everything build, vet, test and gofmt read.
#
# Checksumming the script tells "the gate moved" from "everything else". This
# splits what is left, and the split matters because the two halves want
# opposite responses. If the source is byte-identical across both passes and the
# verdict still flipped, nothing external changed and **the check itself is
# nondeterministic** -- a real defect. Without this it was reported as "shared
# tree; not a result", which is the flattering reading and tells the reader to
# disregard a genuine flaky test. Same bucket problem as php2gotest's
# `unsupported` conflating a gap with a question the oracle could not ask.
#
# Not hypothetical: TESTING.md already records differential cases whose answer
# depends on engine *process* state, which is exactly a check whose verdict
# depends on what ran before it.
input_sha() {
    find . -path ./third_party -prune -o -path ./.agents-workspace -prune -o \
           -path ./.git -prune -o \
           \( -name '*.go' -o -path '*/testdata/*' \) -type f -print0 2>/dev/null \
        | sort -z | xargs -0 sha256sum 2>/dev/null | sha256sum | cut -c1-12
}
input_sha_start=$(input_sha)

# Package arguments are resolved from the repo root, not from the caller's cwd,
# so `hack/gate.sh ./internal/lower/...` means the same thing from anywhere.
pkgs=("${@:-./...}")

# GATE GREEN means "passed once", not "passes reliably".
#
# The re-run below fires only on *failure*, so a check that fails then passes is
# caught (GATE FLAKY) and one that **passes then fails is never looked at**. The
# runtime session constructed it: a test that passes on its first run and fails
# on every run after gives GATE GREEN, exit 0. That the gate has a FLAKY verdict
# at all makes this worse, by suggesting flakiness is covered when it is covered
# in one direction -- and the blind direction is, once again, the flattering one.
#
# Of the four checks only `test` can be nondeterministic: gofmt, build and vet
# are pure functions of the input set. So the asymmetry needs closing for exactly
# one check, and `-count=2` buys it without doubling the other three.
#
# Not the default, decided on measurement rather than taste: `-count` disables
# the test cache, so it turns a 2s gate into a 34s one on every run. Opt in
# before a claim that matters:
#
#     GATE_REPEAT_TESTS=1 hack/gate.sh
# A count rather than a boolean, because two runs only show that run 2 matches
# run 1. The runtime session verified their non-idempotency fix at -count=3 to
# see a fixed point instead of a coincidence, and that check should be reachable
# from here.
test_count="${GATE_TEST_COUNT:-1}"
[ "${GATE_REPEAT_TESTS:-}" = "1" ] && test_count=2   # older spelling
test_flags=()
green_note=""
if [ "$test_count" -gt 1 ] 2>/dev/null; then
    test_flags=(-count="$test_count")
else
    green_note="  (tests ran once; GATE_TEST_COUNT=2 to check they pass twice, 3 for a fixed point)"
fi
failed=()

run() { # run <name> <cmd...>
    local name="$1"; shift
    if ! "$@"; then failed+=("$name"); fi
}

# gofmt reports by *printing*, not by exit status, so it needs its own rule.
# third_party is a pristine submodule; .agents-workspace holds generated and
# throwaway artifacts, which are output rather than source.
unformatted="$(gofmt -l . 2>/dev/null | grep -vE '^(third_party|\.agents-workspace)/' || true)"
if [ -n "$unformatted" ]; then
    echo "unformatted:"; echo "$unformatted"
    failed+=("gofmt")
fi

run build go build "${pkgs[@]}"
run vet   go vet "${pkgs[@]}"
run test  go test "${test_flags[@]}" "${pkgs[@]}"

if [ ${#failed[@]} -eq 0 ]; then
    echo "GATE GREEN${green_note}"
    exit 0
fi

# A single GATE RED on a shared tree is not a result.
#
# Two sessions write this tree, so a check can catch a file between writes or a
# package mid-edit: gofmt reads a half-written file, vet and test compile an
# inconsistent package. The runtime session saw four such REDs in five minutes,
# every one of which resolved on a re-run, with their own files clean throughout.
#
# That is the gate reporting a real state rather than a false positive -- the
# tree genuinely was inconsistent at that instant. It is the same rule the corpus
# already has for a shared *box* ("a measurement with compile errors is noise;
# re-take it"), applied to a shared *tree*.
#
# Re-running is done here rather than left as advice, for the reason everything
# else in this file is structural: a rule is remembered, a gate is not optional.
first=("${failed[@]}")
failed=()
unformatted="$(gofmt -l . 2>/dev/null | grep -vE '^(third_party|\.agents-workspace)/' || true)"
if [ -n "$unformatted" ]; then
    echo "unformatted:"; echo "$unformatted"
    failed+=("gofmt")
fi
run build go build "${pkgs[@]}"
run vet   go vet "${pkgs[@]}"
run test  go test "${test_flags[@]}" "${pkgs[@]}"

if [ ${#failed[@]} -eq 0 ]; then
    if [ "$(self_sha)" != "$gate_sha_start" ]; then
        echo "GATE UNSTABLE: ${first[*]} did not reproduce -- the gate script itself changed mid-run" \
             "($gate_sha_start -> $(self_sha)); not a result"
        exit 2
    fi
    if [ "$(input_sha)" != "$input_sha_start" ]; then
        echo "GATE UNSTABLE: ${first[*]} did not reproduce -- the source changed mid-run" \
             "($input_sha_start -> $(input_sha)); shared tree, not a result"
        exit 2
    fi
    # Nothing external moved, and the verdict still flipped.
    echo "GATE FLAKY: ${first[*]} failed then passed with the source unchanged" \
         "(input $input_sha_start) -- the check is nondeterministic, which is a defect"
    exit 3
fi
if [ "$(self_sha)" != "$gate_sha_start" ]; then
    echo "GATE UNSTABLE: the gate script changed mid-run ($gate_sha_start -> $(self_sha)); not a result"
    exit 2
fi
echo "GATE RED: ${failed[*]} (gate $gate_sha_start)"
exit 1
