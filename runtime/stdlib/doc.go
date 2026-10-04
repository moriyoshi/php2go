// Package stdlib holds PHP's standard library where **both** back-ends can
// reach it.
//
// The split it exists to fix: php2go's library surface was in three places and
// compiled code could reach only one of them. `internal/userland` holds 262
// functions whose bodies are PHP; `runtime/interp` holds 272 implemented in Go;
// `runtime/php` holds the value semantics. An emitted program is its own Go
// module, so it cannot import anything under `internal/`, and it has no reason
// to link an interpreter -- which left it with no standard library at all and
// 470 corpus files refused by name.
//
// Measured, because it decides the design: the userland library **lowers
// completely** -- 444 of 444 op_arrays -- and still cannot be emitted, because
// its PHP bodies call 26 library functions it does not define. `fwrite`,
// `ini_get` and `md5` cannot be written in PHP. So a standard library that is
// mostly PHP still rests on a native core, and the core has to live somewhere
// both consumers can import. That is here.
//
// # What belongs here
//
// A PHP library function implemented in Go. Nothing else:
//
//   - Value semantics, operators and intrinsics stay in `runtime/php` -- they
//     are the language, not the library, and this package imports them.
//   - Anything needing a class table, a call stack it can walk, or re-entry
//     into user code stays with the consumer that has one, reaching this
//     package's shape through a `php.Ctx` hook.
//   - A function whose body can be PHP belongs in `internal/userland`, not
//     here.
//
// Registration goes through `php.RegisterBuiltin`, so the table itself stays
// one mechanism in one place and this package supplies entries for it. A
// consumer links the surface by importing this package for its side effects.
package stdlib
