#!/usr/bin/env bash
# Compiled output against the engine, on the same PHP, by wall clock.
#
# P0 of .agents/docs/OWNERSHIP-PERF.md. Every program goes through the **gogen**
# path -- php2gotest -emit writes it as a module of its own -- because
# `php2go bundle` embeds the interpreter, and profiling a bundle profiles
# `interp.execFrame`. The engine arm is `php2eval -raw`, the engine alone.
#
# Usage:
#   eval "$(hack/cgoenv.sh)" && hack/bench.sh [options] [file.php...]
#
# Options:
#   -n RUNS          runs per arm; the median is reported (default 3)
#   -o DIR           keep modules, outputs and profiles here (default: a temp dir)
#   -p START:DUR     CPU-profile the first compiled run over that window, in
#                    seconds; the window must close before the program exits
#   -E               skip the engine arm
#   -F               emit with -fused-math: native float arithmetic may fuse
#                    into multiply-adds, so results can differ in the last bit
#
# With no files: every hack/bench/*.php, plus Zend/bench.php and
# Zend/micro_bench.php from the submodule with their date_default_timezone_set
# call removed, which compiled output refuses. Each program's own output --
# bench.php prints per-test timings -- is kept under DIR/out.
#
# Wall clock includes start-up on both sides: the engine's is ~20 ms of
# php2eval loading libphp, so a ratio on a program that runs for less than a
# second understates the gap. The programs' own timings, in DIR/out, do not.
set -uo pipefail

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$root" || exit 1

runs=3
out=""
profile=""
engine=1
emitflags=()
while getopts "n:o:p:EF" opt; do
    case $opt in
        n) runs=$OPTARG ;;
        o) out=$OPTARG ;;
        p) profile=$OPTARG ;;
        E) engine=0 ;;
        F) emitflags+=(-fused-math) ;;
        *) sed -n '2,28p' "$0"; exit 2 ;;
    esac
done
shift $((OPTIND - 1))

if [ -z "$out" ]; then
    out=$(mktemp -d "${TMPDIR:-/tmp}/php2go-bench-XXXXXX")
fi
mkdir -p "$out/bin" "$out/src" "$out/out" "$out/prof"
out=$(CDPATH= cd -- "$out" && pwd -P)

inputs=("$@")
if [ ${#inputs[@]} -eq 0 ]; then
    for f in hack/bench/*.php; do
        inputs+=("$f")
    done
    for b in bench micro_bench; do
        # Only the timezone call goes; the benchmark is otherwise the engine's.
        sed '/^if (function_exists("date_default_timezone_set")) {$/,/^}$/d' \
            "third_party/php-src/Zend/$b.php" > "$out/src/zend_$b.php"
        inputs+=("$out/src/zend_$b.php")
    done
fi

echo "building php2gotest and php2eval into $out/bin" >&2
go build -o "$out/bin/php2gotest" ./cmd/php2gotest || exit 1
if [ "$engine" = 1 ]; then
    go build -o "$out/bin/php2eval" ./cmd/php2eval || exit 1
fi

echo "emitting ${#inputs[@]} programs" >&2
"$out/bin/php2gotest" "${emitflags[@]}" -emit "$out/mod" "${inputs[@]}" > "$out/emit.txt"
status=$?
grep -v '^emitted' "$out/emit.txt" >&2
if [ $status -ne 0 ] && ! grep -q '^emitted' "$out/emit.txt"; then
    exit 1
fi

# seconds runs a command and prints its wall clock.
seconds() {
    local start end
    start=$(date +%s.%N)
    "$@" > /dev/null 2>&1
    end=$(date +%s.%N)
    echo "$end - $start" | bc
}

median() {
    sort -n | awk '{ v[NR] = $1 } END { if (NR % 2) print v[(NR + 1) / 2]; else printf "%.3f\n", (v[NR / 2] + v[NR / 2 + 1]) / 2 }'
}

printf '%-28s %10s %10s %9s\n' "program" "compiled" "engine" "ratio"
while IFS=$'\t' read -r kind path dir; do
    [ "$kind" = emitted ] || continue
    name=$(basename "$path" .php)
    tags=()
    if [ -n "$profile" ]; then
        cp hack/bench/prof.go "$dir/prof.go"
        tags=(-tags benchprof)
    fi
    if ! (cd "$dir" && go build "${tags[@]}" -o prog . 2> build.err); then
        printf '%-28s %10s\n' "$name" "BUILD-FAILED" >&2
        cat "$dir/build.err" >&2
        continue
    fi
    # The first compiled run keeps its output, and carries the profile.
    if [ -n "$profile" ]; then
        PROF_OUT="$out/prof/$name.pprof" PROF_START=${profile%%:*} PROF_DUR=${profile##*:} \
            "$dir/prog" > "$out/out/$name.compiled" 2>&1
    else
        "$dir/prog" > "$out/out/$name.compiled" 2>&1
    fi
    compiled=$(for _ in $(seq "$runs"); do seconds "$dir/prog"; done | median)
    eng="-"
    ratio="-"
    if [ "$engine" = 1 ]; then
        "$out/bin/php2eval" -raw < "$path" > "$out/out/$name.engine" 2>&1
        eng=$(for _ in $(seq "$runs"); do seconds sh -c '"$1" -raw < "$2"' _ "$out/bin/php2eval" "$path"; done | median)
        ratio=$(printf '%.1f' "$(echo "scale=3; $compiled / $eng" | bc)")
        eng=$(printf '%.3f' "$eng")
    fi
    printf '%-28s %10.3f %10s %8sx\n' "$name" "$compiled" "$eng" "$ratio"
done < "$out/emit.txt"
echo "modules, outputs and profiles: $out" >&2
