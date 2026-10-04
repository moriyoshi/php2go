<?php
// A list destructured by reference out of a call's by-value result has
// nothing to reference: the engine notices and fetches by value, again for
// each level of a nested list. From a by-reference result it binds.
function f() { return [1, [2]]; }
function &g() { static $a = [1, [2]]; return $a; }
[&$v] = f();
var_dump($v);
[, [&$w]] = f();
var_dump($w);
[&$x] = g();
$x = 10;
var_dump(g());
$r = &f()[1];
var_dump($r);
