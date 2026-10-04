<?php
// The result of `$b = &$a` sent to a by-reference parameter binds `$b`'s
// reference silently, whether the callee is declared before or after the call;
// a by-value parameter gets the value; an ordinary assignment's result is
// refused as not passable by reference.

function early(&$p) { $p = "early"; }
function byval($p) { $p = "lost"; }

$a = 0;
early($b = &$a);
var_dump($a, $b);
late($c = &$a);
var_dump($a, $c);
byval($d = &$a);
var_dump($a, $d);
try { late($e = 5); } catch (Error $ex) { echo $ex->getMessage(), "\n"; }

function late(&$p) { $p = "late"; }
