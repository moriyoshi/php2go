<?php
// **An array's internal pointer survives a copy.** Measured against the engine:
// after `next($a)`, both `$b = $a` and a by-value `array $x` parameter see
// `current()` as the *advanced* element, not the first.
//
// PHP's copy is refcounted and the pointer belongs to the array, so it travels.
// php2go's copy is eager and was dropping it, resetting every copied array to
// its first element -- which stayed invisible until something passed a copy to
// a function that reads the pointer.
$a = [1, 2, 3];
next($a);
var_dump(current($a));

$b = $a;
var_dump(current($b), current($a));

function byValue(array $x) { return current($x); }
var_dump(byValue($a));

// Advancing the copy does not move the original, and vice versa.
next($b);
var_dump(current($b), current($a));

// Through a dynamic call, which is where this surfaced: `$f = "current"`.
$f = "current";
$g = "next";
var_dump($f($a));
$g($a);
var_dump($f($a));

// A pointer run off the end stays off the end across a copy.
$c = [1, 2];
next($c); next($c);
var_dump(current($c));
$d = $c;
var_dump(current($d));

// reset() and end() still work on a copy.
$e = [10, 20, 30];
next($e);
$h = $e;
var_dump(current($h));
reset($h);
var_dump(current($h), current($e));
var_dump(end($h), key($h));

// An array with a hole: the pointer maps past removed buckets.
$k = [1, 2, 3, 4];
unset($k[1]);
next($k);
var_dump(current($k));
$l = $k;
var_dump(current($l));
echo "done\n";
