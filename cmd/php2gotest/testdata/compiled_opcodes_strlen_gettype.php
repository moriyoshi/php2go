<?php
// The compiler specializes a handful of library calls into their own opcode, so
// they never reach the library table and need an implementation on this side.

// `strlen` is the ordinary string coercion followed by a byte count, not a
// length with rules of its own: an int has a length, an object with
// `__toString` has one, an array is a TypeError, and a null deprecates on the
// way through and arrives as "".
var_dump(strlen("abc"), strlen(""), strlen(123), strlen(1.5), strlen(true), strlen(false));
var_dump(strlen(null));
try { strlen([1]); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { strlen(new stdClass); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

class S { function __toString() { return "hello"; } }
var_dump(strlen(new S));

// The same coercion reaches a *user* function's declared parameter type.
function takesString(string $x) { var_dump($x); }
takesString(new S);
takesString(42);

// `gettype` answers in the OLD vocabulary -- integer, double, boolean, NULL --
// not the one every diagnostic in this project uses.
foreach ([5, 5.5, "abc", true, false, null, [1], new stdClass] as $v) {
    echo gettype($v), "\n";
}

// Multibyte is counted in bytes, not characters.
var_dump(strlen("日本語"));
