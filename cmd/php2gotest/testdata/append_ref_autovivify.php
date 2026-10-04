<?php
// `$c[] = &$x` on a container that is not yet an array autovivifies it and
// appends the reference as its first element -- once. null and an unset
// variable are silent, false deprecates first, and a keyed `$c["k"] = &$x`
// does the same with its key.

$x = [1];
$t = null;
$t[] = &$x;
var_dump($t);
$f = false;
$f[] = &$x;
var_dump(count($f), array_keys($f));
$k = null;
$k["k"] = &$x;
var_dump(array_keys($k));
function g() { $y = 1; $v[] = &$y; $v[] = &$y; var_dump(array_keys($v)); }
g();
