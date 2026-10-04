<?php
// The result of a by-reference function is an operand like any other in
// arithmetic -- plain, compound on a local, on a typed static, and in a
// bitwise or modulo operator -- not a `reference` the operator refuses.

function &ref() { static $a = "3"; return $a; }
function &intRef() { static $n = 4; return $n; }
class Foo { public static int $i = 1; }

var_dump(1 + ref());
$x = 10;
$x -= intRef();
var_dump($x);
Foo::$i += ref();
var_dump(Foo::$i);
var_dump(intRef() % 3, intRef() | 1, intRef() ** 2);
