<?php
// The pure array operations and the frame readers moved from `runtime/interp`
// into the shared `runtime/php`. The sorts and `array_splice` take their array
// **by reference**, so what they rewrite has to be the caller's own array --
// which is the half a move can silently break.
var_dump(array_fill(5, 3, "x"), array_fill_keys(["a", "b"], 0));
var_dump(array_slice([1, 2, 3, 4, 5], 1, 3), array_slice(["a" => 1, 2, 3], 1, 2, true));
var_dump(array_merge([1, 2], ["a" => 3], [4]));
var_dump(array_key_exists("a", ["a" => null]), array_key_exists(0, [1]));
try { array_fill(0, -1, "x"); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }

$a = [3, 1, 2];
sort($a); var_dump($a);
rsort($a); var_dump($a);
$b = ["b" => 2, "a" => 1, "c" => 3];
ksort($b); var_dump($b);
krsort($b); var_dump($b);
asort($b); var_dump($b);
arsort($b); var_dump($b);
$c = [1, 2, 3, 4, 5];
var_dump(array_splice($c, 1, 2, ["x", "y", "z"]), $c);

