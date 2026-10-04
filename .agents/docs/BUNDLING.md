# Bundling PHP projects

`php2go bundle` harvests a project into an inspectable Go module. The generated
launcher uses `go:embed` for the original source bytes, installed dependencies,
harvested units and the engine manifest. Building that module produces a binary
which runs through `runtime/interp` and has no Zend or cgo dependency.

## Create and build a bundle

The entry is interpreted relative to `-root`. With no explicit root, an absolute
or cwd-relative entry uses its own directory.

```sh
eval "$(hack/cgoenv.sh)"
go run ./cmd/php2go bundle \
  -root /path/to/project \
  -o /tmp/my-app-bundle \
  main.php

(cd /tmp/my-app-bundle && CGO_ENABLED=0 go build -o my-app .)
/tmp/my-app-bundle/my-app
```

The output directory must not already exist. The generated `go.mod` uses a
local replacement for the php2go checkout. If the command cannot discover that
checkout, pass `-runtime-root /path/to/php2go`.

## Build with runtime eval compilation

The generated module also has an opt-in hybrid build. From the php2go checkout,
set the CGO environment described in [TESTING.md](./TESTING.md), then build the
generated module with the `php2go_hybrid` Go tag:

```sh
eval "$(hack/cgoenv.sh)"
(cd /tmp/my-app-bundle && go build -tags php2go_hybrid -o my-app .)
```

The hybrid binary links the same Zend build that harvested the bundle. At each
`eval`, Zend compiles the string produced at runtime and the Go interpreter
executes its IR in the caller's current variables and declarations. The loader
rejects a linked Zend build whose version, operand layout, or opcode table does
not match the bundle manifest. Hybrid requests are serialized because the
embedded Zend engine permits one active instance and pins its Go goroutine to
an operating-system thread. Include and require continue to use preharvested
files in the bundle.

Without `-tags php2go_hybrid`, generated bundles remain Zend-free and an eval
string requiring compilation reports `unsupported`. The same tag is available
for Go modules from `php2gotest -emit`. Those modules keep running generated Go
and bridge an unprofiled eval body into the IR interpreter through pointers to
lowered variables. Eval-created names join the generated scope. Dynamic
`include` and `include_once` of regular host files use the same bridge, with
the generated code retaining path resolution and once tracking. PHP treats
these as language constructs, so the generated hook supplies their execution
rather than exposing callable PHP functions. The emitted-code bridge can
persist eval-defined global functions across calls and call supported
generated global functions. It still refuses classes, runtime-included
declarations, virtual stream paths, resumable caller frames, and some
argument-binding shapes. See the current hybrid limits in [TODO.md](./TODO.md).
Literal include sites inside ordinary branches use their lowered Go bodies
while the resolved file matches the build's content hash; a changed file takes
the tagged interpreted route. The branch itself always stays generated Go.
See the testdata README for that build path.

## What is included

Every regular file below the declared root is stored byte-for-byte, including an
installed Composer `vendor/` tree. `.git/` is excluded. Source payloads and
harvested units are named by SHA-256 in the generated module; `index.json`
maps those payloads back to stable virtual names beneath `/app`.

Files with a PHP extension or a PHP opening tag are harvested eagerly. Relative
`include` and `require` operations resolve within the virtual tree, trying
the current working directory before the including file's directory; their
canonical virtual path supplies `*_once` identity. The cwd starts at `/app`,
persists across calls to `App.Run`, and changes through `chdir()`. This also
lets an ordinary Composer-generated `vendor/autoload.php` load packaged
dependencies without special Composer logic.

The source inventory is deliberately broad. Review the project root before
bundling: files such as `.env`, private keys and development fixtures are
included if they are under it.

## Userland extension bodies

`internal/userland` is the catalog for builtin functions whose implementation
can be ordinary PHP. Each catalog entry contains an embedded PHP source, its
stable virtual path, and explicit public-name-to-implementation aliases.

During generation, the source is harvested alongside the project. At startup,
the bundle runtime installs its declarations as hidden library helpers and
aliases only catalogued names that the engine manifest declares. Calls retain
the engine's harvested parameter list and internal-function identity; only the
body comes from PHP.

The catalog currently supplies:

- `is_countable()` and `is_iterable()`, implemented from `is_array()` and
  language-level `instanceof` checks;
- `array_all()`, `array_any()`, `array_find()`, `array_find_key()`,
  `array_change_key_case()`, `array_replace()` and
  `array_replace_recursive()`, implemented with ordinary array operations;
- `array_walk_recursive()`, composed around native `array_walk()` leaf;
- `array_multisort()` as one stable lexicographic permutation over existing
  `usort()`, `uksort()`, array identity and reference-preserving
  `array_merge()` behavior;
- `array_rand()` and `shuffle()` as PHP selection/complement and Fisher-Yates
  algorithms over one shared request-local MT19937 range leaf and a narrow raw
  array key/permutation leaf;
- the ten `array_*diff*` / `array_*intersect*` functions with user
  comparators, whose direct-lookup and sorted-merge algorithms remain PHP over
  reserved callback validation and unstable associated-sort leaves;
- `natsort()` and `natcasesort()`, implemented over native `uasort()` and
  natural-comparison callbacks;
- the aliases `doubleval()`, `join()`, `key_exists()`, `strchr()` and
  `ini_alter()` as calls to already-native canonical functions;
- `str_rot13()`, `fpow()`, `count_chars()`, `strspn()`, `strcspn()`,
  `quoted_printable_encode()`, `quoted_printable_decode()`, `soundex()`,
  `metaphone()`, `hebrev()`, `str_increment()`, `str_decrement()`,
  `stripcslashes()`, `addcslashes()`, `convert_uuencode()`,
  `convert_uudecode()`, `version_compare()`, `utf8_encode()`,
  `utf8_decode()`, `ini_parse_quantity()`, `parse_url()`, the decimal/base
  conversion family, the four URL encode/decode functions, `escapeshellarg()`
  and `escapeshellcmd()`, plus `parse_str()` query registration and the
  shared `parse_ini_string()` / `parse_ini_file()` scanner,
  `pack()` / `unpack()` byte-format grammar and `sscanf()` conversion
  scanner, as pure byte-string, parser or numeric algorithms;
- `image_type_to_extension()`, `image_type_to_mime_type()`, `ip2long()`,
  `long2ip()`, `inet_pton()` and `inet_ntop()` as fixed lookup or strict
  IPv4 conversion functions;
- `connection_aborted()`, `connection_status()`, `ignore_user_abort()`, and
  both `php_ini_*_files()` readers as embed-SAPI request/startup state, with no
  host ini or client-connection capability;
- `get_current_user()`, `getlastmod()` and `getmyinode()` as the linked
  embed-SAPI's fixed no-translated-request-path answers, plus `getmypid()`,
  `getmyuid()` and `getmygid()` over one live process-identity leaf;
- `gettimeofday()`, `microtime()`, `hrtime()`, `sleep()`, `usleep()`,
  `time_nanosleep()` and `time_sleep_until()` over one wall/monotonic/sleep
  leaf, with formatting, validation, diagnostics and return shapes in PHP;
- `get_cfg_var()` and `phpversion()` over the finite embed startup-config and
  linked-module registries, with versions supplied by `PHP_VERSION`;
- `clearstatcache()` as a validated no-op over direct lookups, plus the two
  last-HTTP-response-header functions as null state without an HTTP wrapper;
- `is_uploaded_file()` and `move_uploaded_file()` as validated false paths
  without an embed-SAPI RFC1867 upload registry or multipart subsystem;
- `get_browser()` as the immutable absent-browscap warning-and-false path;
- `stream_resolve_include_path()` over one loader-aware canonical-path leaf,
  without creating stream resources or exposing wrapper lifecycle;
- `password_algos()`, `password_get_info()` and
  `password_needs_rehash()` as metadata/policy over this build's bcrypt-only
  registry; `get_include_path()` and `set_include_path()` use modeled INI state,
  and `ini_restore()` uses one reset-only state leaf;
- `assert_options()` over one request-local option record consumed by native
  `assert()`, including callback, warning, exception, active and bail policy;
- `fgetc()`, `fpassthru()`, `stream_get_contents()`,
  `stream_copy_to_stream()` and `stream_get_line()`, plus the shared
  `str_getcsv()` / `fgetcsv()` / `fputcsv()` byte grammar and `fscanf()`
  over the same scanner as `sscanf()`, implemented over an already-valid
  stream using native line-read, read, write, seek, output and string
  primitives;
- `stream_set_blocking()`, `stream_set_timeout()`, both buffer controls and
  `stream_set_chunk_size()` over one reserved stream-option leaf, with
  `set_file_buffer()`, `socket_set_blocking()` and
  `socket_set_timeout()` sharing those PHP bodies and
  `socket_get_status()` wrapping native metadata;
- `fflush()`, `fsync()`, `fdatasync()`, `ftruncate()`, `fstat()`,
  `flock()` and `stream_supports_lock()` over one reserved payload-operation
  leaf. Validation, sync warnings, the 26-entry stat shape and lock
  operation/by-reference policy remain PHP;
- `stream_select()` over one readiness leaf for existing file descriptors and
  RFC2397 memory handles. PHP owns by-reference filtering, keys, invalid
  resources, time validation and alias write-back;
- `stream_socket_pair()`, `stream_socket_get_name()`,
  `stream_socket_recvfrom()`, `stream_socket_sendto()`,
  `stream_socket_shutdown()` and `stream_socket_enable_crypto()` over connected
  descriptors. They participate in ordinary stream metadata, options,
  selection, stat, context and I/O behavior;
- `stream_socket_server()`, `stream_socket_client()`,
  `stream_socket_accept()`, `fsockopen()` and `pfsockopen()` over the same
  socket-resource leaf for TCP, UDP, Unix stream and Unix datagram transports.
  PHP owns address/flag policy, finite timeout checks, output references,
  validation and public diagnostics. `pfsockopen()` leases an App-scoped pool,
  reuses the same resource within one request and evicts it on explicit close;
- `stream_get_wrappers()`, `stream_get_transports()` and
  `stream_get_filters()` as fresh arrays of the linked startup registrations;
  no mutable registry or runtime leaf was added, and wrapper/filter lifecycle
  remains pending as one coupled capability;
- all eight `stream_context_*()` functions over one reserved context-state leaf;
  PHP owns nested option/parameter normalization, overload diagnostics and
  result shaping, while the runtime retains identity, ordered merges, default
  sharing and lazy independent contexts for existing streams;
- `stream_is_local()` as PHP wrapper-scheme parsing over the fixed linked
  registry and existing metadata, plus `stream_isatty()` over one `TCGETS`
  predicate in the existing stream-operation leaf;
- `parse_ini_file()` over one diagnostic-free byte read. The optional loader
  source interface serves checksum-validated embedded assets before the
  existing host-file fallback, and the PHP parser owns the public diagnostic.
- `file()`, `readfile()`, `md5_file()` and `sha1_file()` over that same
  byte read. Include-path search, line shaping, output, hashing, binary digest
  conversion, context validation and public diagnostics remain PHP.
- `iptcparse()` and `iptcembed()` as IPTC/JPEG byte grammars over the same
  diagnostic-free byte read. Record parsing, APP13 rewriting, padding, spool
  behavior and public diagnostics remain PHP; no new runtime leaf was added.
- `get_meta_tags()` as its exact small HTML-token scanner over the same byte
  read. Recovery, name normalization, token caps and diagnostics remain PHP;
  neither a general tokenizer nor another filesystem boundary was added.
- `file_exists()`, `is_dir()`, `is_executable()`, `is_file()`, `is_link()`,
  `is_readable()`, `is_writable()` and `is_writeable()` over one loader-aware
  type/access leaf. PHP keeps validation and wrapper diagnostics; full stat,
  mutation, directory handles and stream resources remain outside the leaf;
- `stat()`, `lstat()`, `fileatime()`, `filectime()`, `filegroup()`,
  `fileinode()`, `filemtime()`, `fileowner()`, `fileperms()`, `filesize()`,
  `filetype()` and `linkinfo()` over one loader-aware raw-stat leaf. Host paths
  expose exact linked syscall fields; embedded files expose exact size plus
  stable immutable type/mode metadata without build-host identity or clocks;
- `getrusage()` over one read-only process-usage leaf, plus `ftok()` composed
  from the existing raw-stat leaf and the disk-space family over one
  loader-aware filesystem-capacity leaf. PHP owns public validation, warnings,
  aliases, mode selection and key/result shaping; `sys_getloadavg()` is absent
  from the linked build and remains pending;
- `memory_reset_peak_usage()` as a validated no-op over php2go's identical
  fixed current and peak memory counters; no allocation state was added;
- `gethostname()` over corrected native `php_uname('n')`,
  `sys_get_temp_dir()` over request-cached `TMPDIR`, and `dl()` as the linked
  build's fixed warning/false path; no host or module-loader interface was added;
- `ob_implicit_flush()` as a validated no-op because the embed output sink is
  already unbuffered below explicit PHP buffers; no SAPI flush state was
  added;
- `ob_get_status()` and `ob_list_handlers()` over one read-only stack snapshot;
  PHP shapes the public arrays while the runtime retains exact callable names,
  flags, phase transitions, chunk configuration, logical capacity and usage;
  no SAPI flush state or stream-resource capability was added;
- `getimagesize()` and `getimagesizefromstring()` over one complete image-byte
  parser. The path form reuses checksum-validated embedded reads before host
  fallback; GIF, JPEG APP markers, PNG, SWF, PSD, BMP, both TIFF orders, JPC,
  JP2, IFF, WBMP, XBM, ICO, WebP and AVIF item-property association need no
  decoder or host library. The linked build has no static zlib image handler,
  so compressed SWF deliberately emits its linked notice and returns false;
- `highlight_string()`, `highlight_file()`, `show_source()` and
  `php_strip_whitespace()` over one PHP lexical scanner. The file forms reuse
  checksum-validated embedded reads; the string form needs no bundled compiler.
  Six Zend-compatible token classes drive span colors or comment/whitespace
  stripping, while the five highlight colors share the live INI state;

`internal/userland/surface.tsv` accounts for every public global function in
php-src's `main` and `ext/standard` stubs. It is generated from those stubs,
the runtime's native dispatcher registrations and the userland catalog:

```sh
go generate ./internal/userland
go run ./internal/userland/cmd/surfacegen -root . -check
```

The current 549-function surface is 267 native, 261 userland, 6 pending and 15
unavailable in the linked build. Available-function coverage is 528/534
(98.9%).
The generated file is a coverage ledger, not a correctness claim: each function
still needs an engine differential before its disposition moves to userland.

php-src has no `ext/streams` module: its capability-owning stream layer lives in
`main/streams`, with public functions exposed largely by `ext/standard`.

The locale boundary is one serialized libc process state. `setlocale()`
mutates or queries it and the change persists across `App.Run` requests;
`strcoll()`, locale sorting, `strptime()`, `localeconv()` and
`nl_langinfo()` read the same state. Bundle startup initializes only
`LC_CTYPE` from the environment, matching the linked embed SAPI's C/C.UTF-8
split. Host locale availability remains platform configuration rather than
embedded data, and non-CGO/Windows builds report this native capability
unsupported.

This boundary is intentionally function-only. Two reserved diagnostic
primitives, `PHP2Go\\Internal\\deprecated()` and
`PHP2Go\\Internal\\warning()`, let a replacement raise the engine's
`E_DEPRECATED` or `E_WARNING` rather than PHP's
distinct `E_USER_*` levels. They carry only a message, return `null`, and
attribute the diagnostic to the public caller by walking out of hidden library
frames. A third, `PHP2Go\\Internal\\array_id()`, exposes stable array identity
for recursion guards and for deduplicating aliased `array_multisort()` columns
in otherwise-userland composites.
A fourth, `PHP2Go\\Internal\\formatted_print()`, exposes the existing format
language with an explicit diagnostic offset and array/variadic mode; stream
validation and writes remain PHP.
Two further callback leaves complete the user-comparator set family:
`PHP2Go\\Internal\\validate_callback()` preserves the public function's
callback diagnostic, and `PHP2Go\\Internal\\unstable_sort()` returns an
associated copy using Zend's observable comparator order. The public merge,
duplicate and output logic remains in PHP.

`PHP2Go\\Internal\\mt_range()` draws an unbiased inclusive integer from the
request-local default MT19937 stream. That stream is shared by `mt_rand()`,
`rand()`, `str_shuffle()`, `array_rand()` and `shuffle()`; seeding, legacy mode,
rejection sampling and draw counts therefore stay coherent across public calls.
`PHP2Go\\Internal\\array_random()` exposes only the array representation facts
PHP cannot express: sampling a live key from the effective bucket span, and
rebuilding a numeric permutation while preserving raw reference boxes. Middle
tombstones remain observable, deleted tail buckets do not, and the public
selection/complement and Fisher-Yates algorithms stay in PHP.

`PHP2Go\\Internal\\clock()` exposes microsecond Unix wall time, nanoseconds
from one arbitrary monotonic origin and sleeping by seconds/nanoseconds. PHP
owns the eight-digit `microtime()` string, `gettimeofday()` fields, monotonic
array split, validation, past-target warning and public return values. Sleeps
are uninterruptible because this runtime has no PHP signal delivery; remaining
signal-interruption arrays and `uniqid()` collision state are not claimed.

`PHP2Go\\Internal\\process_identity()` exposes only the host process ID, real
user ID and real group ID. The three script-identity facades remain PHP fixed
answers because the embed SAPI has no translated request path; they do not
consult virtual-file metadata. No process control or enumeration
capability is implied.

`PHP2Go\\Internal\\ini_restore()` deletes only the runtime overrides whose
behavior php2go already models and resets the active error-reporting mask to its
harvested default. The linked embed build uniquely leaves an `include_path`
override in place; unknown directives remain no-ops. The leaf does not enumerate
or claim the broader INI registry required by `ini_get_all()`.

`PHP2Go\\Internal\\assert_options()` is a get/set boundary for one request-local
record. PHP validates option numbers and parses legacy INI booleans; native
`assert()` consumes active, callback, bail, warning and exception state. The
public deprecation runs before argument validation, matching the engine even for
missing, excess and invalid-option calls.

The diagnostic-free `PHP2Go\\Internal\\read_file()` leaf lets PHP
composites own their public warnings while consulting embedded, RFC2397 or host
source bytes. It now supports `parse_ini_file()`, `get_meta_tags()`,
`iptcembed()` and the four file-content functions without growing into a
generic filesystem or stream-resource API. Absolute paths outside `/app`
defer to the established
host fallback; malformed or escaping virtual paths remain hard errors.
RFC2397 plain payloads use form decoding while base64 payloads are decoded raw.
`PHP2Go\\Internal\\resolve_include_path()` exposes only canonical existence
resolution. The optional loader contract distinguishes owned embedded paths
from host fallback, recognizes directories derived from indexed files and
keeps virtual-root escapes blocked. Its wrapper parsing reproduces the linked
registry's false and unknown-wrapper warning paths without opening any wrapper.

`PHP2Go\\Internal\\path_predicate()` exposes only existence, type and POSIX
access facts. Its optional loader contract distinguishes owned embedded paths
from host fallback; indexed sources are readable immutable files and derived
directories are readable/searchable immutable directories. Link identity stays
separate from followed target facts, including broken symlinks. Full metadata
is exposed separately, leaving this predicate leaf free of stat arrays,
mutation and resources.

`PHP2Go\\Internal\\path_stat()` returns the linked 13 raw fields for host
paths and deterministic metadata for embedded files and derived directories.
The virtual contract preserves exact file size and immutable regular/directory
modes while fixing device identity to the virtual filesystem and leaving
inode, ownership and timestamps at zero; this avoids making bundle output
depend on the build host. PHP owns link-following policy, the 26 numeric/alias
array shape, file-type names, NUL rules and public diagnostics. `linkinfo()`
intentionally bypasses wrapper parsing, as the linked engine does.

`PHP2Go\\Internal\\resource_usage()` exposes exactly one `getrusage(2)` record,
selecting children only for public mode 1. PHP retains the public mode policy and
manifest validation. `PHP2Go\\Internal\\path_space()` returns available and
total bytes from `statfs(2)`; loader-owned files use the generated executable's
backing filesystem. PHP owns empty/NUL rules, function-specific warnings,
`diskfreespace()` alias naming and free/total selection. `ftok()` needs no new
leaf: PHP combines the existing raw device and inode fields with the one-byte
project ID and performs the signed 32-bit conversion.

The two representation leaves `float_bytes()` and
`bytes_float()` bit-cast only 32- or 64-bit IEEE-754 values with native,
little or big byte order. The `pack()` / `unpack()` grammar, integer
wrapping and diagnostics stay in PHP.

The reserved `PHP2Go\\Internal\\stream_set_option()` leaf is the only
mutation boundary for blocking, timeout, buffer and chunk-size controls.
Public validation, return shaping and aliases remain PHP.
`stream_get_meta_data()` stays native because only the resource payload can
report wrapper identity, mode, EOF and seekability.

The reserved `PHP2Go\\Internal\\stream_operation()` leaf is the payload
boundary for flush, sync, truncate, stat, advisory locks and the TTY query. It
returns raw results only; public warnings, validation, stat field aliases,
`flock()`'s by-reference output and `stream_isatty()` shaping remain PHP. The
TTY branch is only one `TCGETS` predicate; RFC2397 truncate shares the leaf.

The reserved `PHP2Go\\Internal\\stream_select()` leaf maps only already-owned
file, socketpair and RFC2397 memory handles to readiness. PHP rebuilds the
three by-reference arrays, retains original keys, classifies invalid values,
validates nullable timeouts and applies aliased-argument write-back order.
Duplicate handles count once in each read/write/exception set. The leaf creates
no socket, wrapper, filter or transport resource.

The reserved `PHP2Go\\Internal\\stream_socket()` leaf owns socket descriptors,
address parsing and the TCP, UDP, Unix stream and Unix datagram lifecycle. It
creates socketpairs, bound/listening servers, synchronous or asynchronous
clients and accepted streams, and exposes raw name, receive, explicit-destination
send and shutdown operations. All descriptors share ordinary stream reads,
writes, selection, blocking, receive timeouts, metadata, stat, EOF and contexts;
created and accepted streams share an explicitly supplied context by identity.
PHP owns public resource validation, flags, finite timeout checks, legacy
`fsockopen()` address shaping, errno/message output references, accept-peer
output, diagnostics, shutdown modes and the linked build's no-SSL crypto policy.
A bundle `App` owns persistent socket entries across fresh interpreter requests;
each request leases an idle entry, concurrent requests receive separate leases,
and explicit close removes the entry rather than returning it to the pool.

The reserved `PHP2Go\\Internal\\stream_context()` leaf owns only context
resource identity plus nested option and notification state. The eight public
functions normalize arrays, enforce overload/resource diagnostics and shape
results in PHP. One request-local default context is shared by the default
functions, while existing file, RFC2397 and socket streams allocate
independent context state lazily.

Extension classes, constants, resources, INI/module lifecycle, native callbacks
and host I/O need additional runtime contracts. A function whose body cannot
be expressed with supported PHP primitives remains an honest unsupported
builtin.

Stream wrappers, filters, mutable registry state and real crypto remain native
capabilities. Context identity/options/params, readiness, locality, TTY
detection, metadata, generic controls and file-like operations are implemented
for existing file, RFC2397 and socket payloads through the leaves above.
Userland remains unsuitable for making or defining wrapper resource classes.

## Current boundaries

- The entry is a CLI program. There is no HTTP request/SAPI adapter yet.
- Arbitrary `eval()` is reported as unsupported in an ordinary bundle because
  that binary has no PHP compiler. The hybrid build can compile runtime eval
  strings within the limits above.
- Includes outside `/app` and stream-wrapper URLs are unavailable.
- Symlinks are rejected so an external target cannot be included accidentally.
- The project-root `.php2go/` path is reserved for bundled runtime libraries.
- A PHP-looking file which cannot be compiled independently stops the build.
- Native Composer platform dependencies such as `ext-*` are runtime
  capabilities and are not supplied by embedding their package metadata.

The bundle runtime validates source and unit checksums when payloads are read.
The original sources remain available through `bundle.App.Source`; execution
uses the harvested units.
