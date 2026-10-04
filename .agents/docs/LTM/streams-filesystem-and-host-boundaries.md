# Streams, Filesystems and Host Boundaries

## Summary

Bundles have two path domains: immutable embedded files served by the loader
and host paths served by native facilities. PHP policy chooses wrappers,
normalizes arguments and shapes diagnostics; native leaves own resources,
syscalls and process-global state. A public function is rarely identical to one
syscall even when its final operation is native.

## Loader and native legs

The bundled loader owns content-addressed source bytes, stable virtual names,
canonical include identity and deterministic metadata for embedded files. Host
files retain the linked platform's libc behavior. A function that accepts both
must select the wrapper/domain before crossing the native boundary.

Embedded files can answer byte reads, existence, type, size and deterministic
mode information. They cannot honestly answer host inode ownership, mutable
timestamps, descriptors, links or filesystem capacity. Mutations stay on the
native leg and reject or bypass embedded paths according to measured PHP
policy.

`chdir` is shared state: it updates native cwd for host operations and the
bundle's virtual cwd for embedded resolution. The linked embed engine keeps it
across requests, so resetting it per `App.Run` would be cleaner and wrong.

## Stream resources

A stream is a live resource with ownership, position and wrapper metadata, not
an eager byte slice. Directory handles, pipes, socket pairs, network sockets,
memory/data streams and ordinary files share resource validation while keeping
their distinct close protocols.

- `popen` resources must be released with `pclose` so their child status is
  collected; treating the descriptor as an ordinary file leaks the process
  protocol.
- directory iteration is live and ordered by the native handle; `scandir` may
  snapshot, `opendir` may not.
- stream options and payload operations are narrow leaves on an already-valid
  stream. PHP owns argument rules and warnings.
- readiness selection preserves caller keys and by-reference filtering while a
  native leaf reports descriptor readiness.
- user wrappers and filters add request-local registries plus stream-local
  callback chains. Registration alone is not implementation of I/O.

Resource declarations in a harvested signature do not prove a live
`php_stream`. Public functions validate the resource kind explicitly and name
the expected handle type in their error.

## Filesystem operations are transactions

PHP adds policy around libc:

- `copy` resolves wrappers, checks source/destination relationships and streams
  the bytes;
- `rename` handles `EXDEV` as a copy/metadata/unlink transaction for eligible
  native files;
- recursive `mkdir` walks path components but each mutation stays native;
- `link` and `symlink` pass original path bytes with different existence rules;
- stat, realpath and include-path behavior share loader-aware lookup but have
  different cache and diagnostic contracts.

The realpath cache is observable state, including entry fields and
invalidation. It belongs with virtual-cwd/native resolution, not as an empty
facade in PHP.

## Process-global state

Some PHP APIs wrap state owned by libc for the entire process. Calls are
serialized, and request policy is measured rather than assumed:

| State | Lifetime rule |
|---|---|
| locale | shared live libc locale; readers and sorters use the same mutation |
| syslog session | serialized for the logical request |
| umask | query through libc; a request change is undone at teardown |
| cwd | retained across bundle requests, matching the linked embed SAPI |
| process priority | direct `nice`; `errno`, not a `-1` return alone, signals failure |
| clocks/sleep | one wall/monotonic/sleep boundary with PHP shaping above it |

Non-CGO or unsupported platforms report an honest capability gap. They do not
silently substitute Go behavior whose errno, locale or descriptor semantics
differ.

## Build-derived tables and fixed state

Entity translation tables, available wrappers/transports/filters, INI
directives, module versions and image dispatch are properties of the linked
build. Harvest or generate them from that build. Source tables and headers can
contain aliases, disabled entries or branches the linked binary does not expose.

Conversely, an absent embed-SAPI facility can have a stable public answer:
uploads are never registered, browscap is absent, no client connection can
abort, and some output-flush APIs are no-ops below PHP's explicit buffer stack.
These are measured facts about this SAPI, not generic PHP rules.

## Relevant files

- `runtime/bundle/bundle.go`, `internal/bundlebuild/builder.go` — virtual files
  and project packaging.
- `runtime/interp/streams.go`, `streamwrappers.go`, `streamfilters.go`,
  `realpath.go` — PHP-facing resources and state.
- `runtime/libc/*_unix.go` and platform stubs — native capability leaves.
- `internal/userland/ext/streams.php`, `ext/standard.php` — PHP policy and
  composites.

## Test coverage

Native leaves have focused `runtime/libc` tests; public behavior is checked by
engine differentials and bundle tests over both embedded and host paths.
Request-lifetime tests cover teardown and state leakage where a one-shot eval
cannot.

## Pitfalls

- Do not infer linked support from php-src source files.
- Do not use `os.File` semantics for pipes, directories or sockets without
  checking the resource's close and metadata protocol.
- Do not expose invented host facts for embedded files.
- Do not reset process state merely because requests normally look isolated;
  probe whether the embed SAPI retains or restores it.
- Wrapper selection precedes filesystem mutation. A correct syscall on the
  wrong path domain is still wrong.

## DNS and response-header composites

DNS is PHP policy over existing request and stream state, not a new resolver
capability in the runtime. Build configuration enables the linked engine's
resolver features so it remains the oracle; harvested PHP constructs and parses
DNS packets, reads resolver configuration through the silent file seam, and
uses existing UDP/TCP stream sockets. UDP reception must use the datagram
operation (`stream_socket_recvfrom`) because an oversized stream-style read can
truncate the packet; TCP is used when the DNS truncation flag requires it.

The embed SAPI reports `headers_sent() === true` from startup yet still accepts
header changes because it has no transport headers to emit. Public header and
cookie functions therefore maintain an ordered, request-local `$_RESP_HEADERS`
array in PHP, initialized with the linked version header and registered as a
superglobal for active and future frames. Replacement is case-insensitive,
duplicates remain ordered, and status/Location/WWW-Authenticate behavior stays
in the PHP composite rather than a second native header model.
