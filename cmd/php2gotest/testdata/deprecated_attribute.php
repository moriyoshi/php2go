<?php
// PHP 8.4's `#[\Deprecated]`. Using such a function or method emits
// `E_USER_DEPRECATED`, and the reason follows the whole phrase rather than the
// name: `Function g() is deprecated, use g2()`.
//
// The calls are deliberately **on one source line**. The emitter writes one
// `SetLocation` per line, so after `f()` returns the location was left wherever
// `f`'s body put it, and `g()`'s diagnostic reported a line inside a function
// that had already returned. `PopCall` restores the call site now, which is
// what returning from a call means.
#[\Deprecated] function f() {}
#[\Deprecated("use g2()")] function g() {}
class C { #[\Deprecated] public function m() {} #[\Deprecated("why")] public static function s() {} }
f(); g(); (new C)->m(); C::s();
// Constants and enum cases carry the attribute too, and the noun differs:
// a constant says `Constant`, an enum case says `Enum case`.
class K {
    #[\Deprecated] const OLD = 1;
    #[\Deprecated("use NEW")] const OLDER = 2;
    const FINE = 3;
}

enum Suit {
    #[\Deprecated] case Clubs;
    case Hearts;
}

var_dump(K::OLD);
var_dump(K::OLDER);
var_dump(K::FINE);
var_dump(Suit::Clubs);
var_dump(Suit::Hearts);

// The error *number* separates the attribute from the engine's own
// deprecations: E_USER_DEPRECATED is 16384, E_DEPRECATED is 8192, and a handler
// sees which one it got.
set_error_handler(function (int $errno, string $errstr) {
    echo "handler saw ", $errno, ": ", $errstr, "\n";
    return true;
});
K::OLD;
f();
restore_error_handler();

// The `since:` argument renders before the message, in the engine's order:
// `is deprecated since 2.0, use b2()`.
#[\Deprecated(since: "1.0")] function sinceOnly() {}
#[\Deprecated("use other()", "2.0")] function sinceAndReason() {}
#[\Deprecated(message: "named", since: "3.0")] function namedArgs() {}
class Versioned { #[\Deprecated(since: "4.0")] const OLD = 1; }
sinceOnly();
sinceAndReason();
namedArgs();
var_dump(Versioned::OLD);

// The attribute's arguments are `?string` **constructor arguments**, not string
// fields, and they take the ordinary coercion path. Weak mode stringifies a
// non-string; `strict_types=1` would raise
// `Deprecated::__construct(): Argument #1 ($message) must be of type ?string,
// int given` -- with no `, called in ...` suffix, because the callee is
// internal, and with no frame for the deprecated function, because the
// attribute is evaluated during call setup.
#[\Deprecated(1234)] function numericReason() {}
#[\Deprecated(1.5)] function floatReason() {}
#[\Deprecated("")] function emptyReason() {}
#[\Deprecated(null)] function nullReason() {}
#[\Deprecated(since: "9.9")] function sinceNoMessage() {}

// A **deferred** argument: the constant is defined after the function, so the
// engine keeps the tree and evaluates it where the deprecation fires.
#[\Deprecated(LATE_REASON)] function deferredReason() {}
#[\Deprecated(LATE_REASON, since: LATE_SINCE)] function deferredBoth() {}

define('LATE_REASON', 'defined later');
define('LATE_SINCE', '5.5');

numericReason();
floatReason();
emptyReason();
nullReason();
sinceNoMessage();
deferredReason();
deferredBoth();
