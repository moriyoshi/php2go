<?php
// `[&$x]` binds a reference **into the element**: the element keeps the box
// rather than copying through it, so a later write through either name is
// visible from the other. The engine says which element is meant in
// extended_value's low bit -- `[$c]` is 4 and `[&$c]` is 5.
$c = 5;
$a = [&$c];
$a[0]++;
var_dump($c, $a);
$c = 99;
var_dump($a);
$a[0] = "through the array";
var_dump($c);

// Mixed: only the marked element is bound.
$p = 1;
$q = 2;
$mixed = [$p, &$q, 3];
$mixed[0] = "no";
$mixed[1] = "yes";
var_dump($p, $q, $mixed);

// With a key, and with a string key.
$k = "start";
$keyed = ["slot" => &$k, 7 => &$k];
$keyed["slot"] = "changed";
var_dump($k, $keyed);

// var_dump marks a bound element, and so does a nested one.
$n = 10;
$nested = [[&$n]];
var_dump($nested);
$nested[0][0] = 11;
var_dump($n);

// A bound element survives being copied into another variable: PHP keeps a
// program-visible reference as the same box.
$src = 1;
$one = [&$src];
$two = $one;
$two[0] = 42;
var_dump($src, $one, $two);

// Unpacking passes a bound element **as the box**, so a by-reference parameter
// binds. `call_user_func_array` is the same path: the engine compiles it to a
// SEND_ARRAY, which is an unpack.
function grow(&$n) { $n .= "!"; }
$word = "hi";
$spread = [&$word];
grow(...$spread);
var_dump($word);
call_user_func_array("grow", $spread);
var_dump($word);
// A by-value parameter unboxes, and the caller's variable is left alone.
function look($n) { $n .= "?"; return $n; }
var_dump(look(...$spread), $word);
