<?php
// A static property read reaches the argument as a write target, because the
// fetch does not know yet what the slot wants. It has to be READ there.
class C { public static $x = 1; public static $arr = [1, 2]; }
var_dump(C::$x);
var_dump(C::$arr);
$n = "C";
var_dump($n::$x);
// Mixed with ordinary arguments, in order.
function two($a, $b) { var_dump($a, $b); }
two(C::$x, C::$arr);
two("first", C::$x);
// And an array element, which is a place for the same reason.
$a = ["k" => 5];
var_dump($a["k"]);
echo "done\n";
