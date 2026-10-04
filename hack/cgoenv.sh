#!/bin/sh
# Emits the CGO_* settings needed to build against the Bazel-built libphp.
#
#   eval "$(hack/cgoenv.sh)" && go build ./...
#
# The generated headers (php_config.h, zend_config.h, build-defs.h) live in the
# Bazel output tree, not the source checkout, so both roots are on the include
# path.
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd -P)
src=$root/third_party/php-src

bazel=${BAZEL:-bazel}
# BAZEL_STARTUP carries startup flags such as --output_user_root, which have to
# match whatever the build used or bazel-bin points somewhere else entirely.
# shellcheck disable=SC2086
bin=$("$bazel" ${BAZEL_STARTUP:-} info bazel-bin 2>/dev/null || true)
if [ -z "$bin" ]; then
    echo "cgoenv: could not determine bazel-bin; run a build first" >&2
    exit 1
fi

# php-src is consumed as an external repository so its checkout stays pristine.
gen=$bin/external/+new_local_repository+php
if [ ! -d "$gen" ]; then
    echo "cgoenv: $gen missing; run: $bazel build @php//:sapi_embed_shared" >&2
    exit 1
fi

cflags="-I$src -I$src/main -I$src/Zend -I$src/TSRM -I$gen -I$gen/main -I$gen/Zend"
# rpath so the built binary finds libphp without LD_LIBRARY_PATH.
ldflags="-L$gen -lphp -Wl,-rpath,$gen"

echo "export CGO_CFLAGS='$cflags'"
echo "export CGO_LDFLAGS='$ldflags'"

# Regenerate the IR protobuf bindings (requires buf and protoc-gen-go):
#   go install github.com/bufbuild/buf/cmd/buf@latest
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   (cd proto && buf generate)
# The generated .pb.go is committed so an ordinary build needs neither tool.
