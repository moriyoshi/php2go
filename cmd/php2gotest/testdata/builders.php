<?php
$k = "key";
$v = 42;
$a = [1, 2, $v];
$b = [$k => $v, "lit" => 1, 7 => "seven"];
$c = [];
$nested = [[1, 2], ["x" => [3]]];
$name = "world";
$s = "hello $name!";
$t = "a" . $name . "b";
$u = "n is {$v} and k is $k";
// The array being built is one array, so appending after a key does not reset
// the next free integer.
$mixed = [5 => "five", "next", "another"];
var_dump($a, $b, $c, $nested, $s, $t, $u, $mixed);
