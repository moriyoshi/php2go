#!/bin/sh
# Runs the Bazel-built bison with its own m4 and skeleton directory.
#
# Neither can be left to chance. Bison execs m4 by searching PATH, and it loads
# m4sugar.m4 from a pkgdatadir compiled in at configure time -- a path under the
# rules_foreign_cc output tree that need not exist when a consuming action runs.
# Both are overridable by environment, so point them at the real artifacts.
set -eu

self=$0
case $self in
    /*) ;;
    *) self=$PWD/$self ;;
esac

# A tool used by a genrule is executed directly rather than through the runfiles
# launcher, so RUNFILES_DIR is often unset and the tree has to be found from the
# script's own path. The exec root is a last resort.
roots="$self.runfiles"
if [ -n "${RUNFILES_DIR:-}" ]; then
    roots="$roots ${RUNFILES_DIR}"
fi
roots="$roots $PWD"

find_one() {
    for root in $roots; do
        [ -d "$root" ] || continue
        found=$(find -L "$root" -type f -name "$1" -perm -u+x 2>/dev/null | head -1)
        if [ -n "$found" ]; then
            echo "$found"
            return 0
        fi
    done
    echo "bison_wrapper: could not locate $2; searched:$roots" >&2
    return 1
}

BISON_BIN=$(find_one bison "the bison binary")
M4_BIN=$(find_one m4 "the m4 binary")

# The skeletons sit next to the binary as ../share/bison.
BISON_PREFIX=$(dirname "$(dirname "$BISON_BIN")")
if [ -d "$BISON_PREFIX/share/bison" ]; then
    BISON_PKGDATADIR=$BISON_PREFIX/share/bison
    export BISON_PKGDATADIR
fi

M4=$M4_BIN
export M4
PATH=$(dirname "$M4_BIN"):$PATH
export PATH

exec "$BISON_BIN" "$@"
