<?php
// The maths, the path helpers, the scalar conversions and the internal array
// pointer moved from `runtime/interp` into the shared `runtime/php`, which is
// what gives *compiled* code a route to them. Every call here was `unsupported`
// before the move.
echo chr(65), chr(-1) === "\xff" ? " wrap" : " nowrap", "\n";
var_dump(ord("A"), ord(""));
var_dump(range(1, 5), range("a", "c"), range(0, 1, 0.5), range(5, 1, 2));
var_dump(get_debug_type(1), get_debug_type("x"), get_debug_type(null), get_debug_type(new stdClass));
try { range(1, 2, 0); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }

// The pointer family takes its array by reference, so the pointer it moves is
// the caller's own -- which is the whole reason these cannot be plain values.
$a = [10, 20, 30];
var_dump(current($a), key($a));
var_dump(next($a), key($a));
var_dump(end($a), key($a));
var_dump(prev($a), key($a));
var_dump(reset($a), key($a));
var_dump(next($a), next($a), next($a), key($a));

var_dump(fdiv(1, 0), fdiv(-1, 0), is_nan(fdiv(0, 0)));
var_dump(is_infinite(INF), is_finite(1.0), abs(-3));
var_dump(ceil(1.2), floor(1.8), round(1.955, 2), pow(2, 10), pow(2, -1));
var_dump(fmod(7, 3), hypot(3, 4), intdiv(7, 2));
var_dump(is_scalar(1), is_scalar([]), is_scalar(null));
echo dirname("/a/b/c"), "|", dirname("/a/b/c", 2), "|", basename("/a/b/c.php", ".php"), "\n";
var_dump(pathinfo("/a/b/c.php"));
