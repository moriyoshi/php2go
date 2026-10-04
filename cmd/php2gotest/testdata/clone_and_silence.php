<?php
// clone is SHALLOW: properties are copied, an object a property points at is
// shared.
class C { public $a = 1; public $o; }
$x = new C; $x->o = new C;
$y = clone $x;
$y->a = 2;
var_dump($x->a, $y->a, $x->o === $y->o);
// __clone runs on the clone, not the original.
class D { public $n = 1; public function __clone() { echo "cloned\n"; $this->n = 99; } }
$d = new D; $e = clone $d;
var_dump($e->n, $d->n);
// Cloning a non-object.
$s = "str";
try { $z = clone $s; } catch (Error $err) { echo $err->getMessage(), "\n"; }
// `@` suppresses the warning an ordinary read emits, and only for its own
// expression.
$arr = [];
var_dump(@$arr["nope"]);
var_dump($arr["nope"] ?? "d");
var_dump($arr["loud"]);
echo "done\n";
