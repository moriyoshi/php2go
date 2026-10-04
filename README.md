# php2go

php2go harvests the real Zend compiler's output and runs it in a Go runtime.
It can inspect a PHP file's compiled representation or package a PHP project
as a Go application with its source and compiled units embedded. It is an
experimental implementation: unsupported PHP behavior is reported as such,
and generated programs need testing against the PHP version used to build them.

## Build

You need Go 1.26.5, Bazel, a C toolchain, and the pinned PHP source submodule.
The build uses Bazel to compile the PHP embed SAPI, then `hack/cgoenv.sh` to
point Go at its headers and library.

```sh
git clone --recurse-submodules https://github.com/moriyoshi/php2go.git
cd php2go
bazel build @php//:sapi_embed_shared
eval "$(hack/cgoenv.sh)"
go build ./...
```

If Bazel is installed under another name or uses a custom output root, set
`BAZEL` and `BAZEL_STARTUP` before running the build and `cgoenv.sh`. See the
[testing guide](.agents/docs/TESTING.md) for an example.

## Try it

`php2go dump` shows the harvested structure without running the program:

```sh
go run ./cmd/php2go dump path/to/index.php
go run ./cmd/php2go dump -o program.pb path/to/index.php
go run ./cmd/php2go dump -decode program.pb
```

`php2go bundle` writes a new Go module. Its output directory must not exist
yet. By default, the entry file's directory is the project root; use `-root`
when includes or Composer dependencies live elsewhere. With `-root`, give the
entry path relative to that root.

```sh
go run ./cmd/php2go bundle -root ./my-app -o ./build/my-app index.php
(cd ./build/my-app && CGO_ENABLED=0 go build -o app .)
./build/my-app/app
```

The ordinary bundled program runs through the Go interpreter without Zend.
The generated module refers to this checkout through a local `go.mod` replace
directive. Keep the checkout available when building the generated module, or
edit that directive to point at a published version.

For runtime eval strings, build a bundle with `-tags php2go_hybrid` and the
same CGO environment used for the compiler. This opt-in path links Zend to
compile strings reached at runtime. The hybrid bridge has documented limits;
see [bundling details](.agents/docs/BUNDLING.md).

Run `go run ./cmd/php2go --help` and `go run ./cmd/php2go bundle --help` for
command options and examples. `php2go profile` records eval strings reached
in one reference run; the profile is consumed by `php2gotest -eval-profile`,
not by `bundle`.

## Development

`php2eval` runs a small script through Zend and the interpreter side by side.
`php2gotest` checks emitted Go against the Zend oracle, while `php2phpt`
grades the interpreter against PHP's `.phpt` cases. The [project overview](.agents/docs/OVERVIEW.md),
[testing guide](.agents/docs/TESTING.md), and [open work](.agents/docs/TODO.md)
describe the architecture, required environment, and known gaps.

After setting the CGO environment, run `hack/gate.sh` for formatting, build,
vet, and Go tests. A reported `unsupported` case is a known gap; it must not be
counted as a passing program.

## License and third-party code

The original php2go code is available under the [MIT License](LICENSE).
The PHP submodule and the Go Protocol Buffers dependency have separate
licenses and notices; see [NOTICE](NOTICE) and [licenses/](licenses/). A
binary linked with PHP or Zend must carry the applicable upstream license
texts and acknowledgments. php2go is not affiliated with or endorsed by the
PHP Group or Zend Technologies. Before publishing Zend-linked binaries, review
the pinned PHP License's product-name condition and the component-specific
notices listed in [open work](.agents/docs/TODO.md).
