<?php
function f($a, $b, $c) { var_dump($a, $b, $c); }
$args = [1, 2, 3];
f(...$args);
// Unpacking mixes with ordinary arguments, in order.
f(1, ...[2, 3]);
f(...[1, 2], ...[3]);
function g(...$all) { var_dump($all); }
g(...[1, 2], ...[3]);
g();
// Unpacking something that is not a container.
try { f(...5); } catch (\Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "done\n";
