<?php
// min() and max() are two functions in the engine. A call with two arguments
// is frameless, with its own ties: min answers the left only when it is
// strictly smaller, max whenever it is not smaller. Any other call walks its
// arguments -- ints and floats on their own paths, the rest through
// zend_compare, a tie keeping the earlier -- or searches the one array it is
// given, where an element uncomparable with the running answer replaces it
// for min and not for max.

$f = 5; $s = "5"; $p = ["a" => 1]; $q = ["b" => 1]; $h = 1.5; $t = 2; $hs = "1.5";
var_dump(min($f, $s), min($s, $f), max($f, $s), max($s, $f));
var_dump(min($p, $q), min($q, $p), max($p, $q), max($q, $p));
var_dump(min($t, $h, $hs), min($f, $s, $f), max($s, $f, $s));
var_dump(min($p, $q, $p), max($q, $p, $q));
var_dump(min([$p, $q]), min([$q, $p]), max([$p, $q]), max([$q, $p]));
var_dump(min(1.0, 1), min(1, 1.0), max(1, 1.0), max(1.0, 1));
var_dump(min([1, 1.0]), max([1.0, 1]), min(3, 2.5, 7), max(1, 2.5, "3"));
var_dump(min([3, 1, 2]), max([3, 1, 2]), min([[1, 3], [1, 2]]), max(['k' => 'b', 'j' => 'a']));
var_dump(call_user_func('min', $f, $s), call_user_func('max', $p, $q));
var_dump(min(PHP_INT_MAX, (float)PHP_INT_MAX), max(PHP_INT_MAX, (float)PHP_INT_MAX));
var_dump(min(NAN, 1), min(1, NAN), max(NAN, 1), max(1, NAN));
var_dump(max('abc', [0]), min('abc', [0]));
try { min([]); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
try { max(1); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { max(true); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
