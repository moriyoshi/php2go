<?php
class P { public $d = 1; }
$p = new P;
$p->nope = 2;   // deprecated: creation
$p->nope = 3;   // silent: not a creation
class R { public $d = 1; public function go() { $this->inner = 1; } }
(new R)->go();  // deprecated from inside the class too
$s = new stdClass; $s->x = 1;
#[AllowDynamicProperties] class Q {}
class Qq extends Q {}
$q = new Qq; $q->z = 1;
class S { public function __set($n, $v) { echo "set($n)\n"; } }
$ss = new S; $ss->x = 1;
class T { public $d = 1; }
$t = new T; unset($t->d); $t->d = 2;   // re-writing a declared one is no creation
class E2 extends Exception {}
$e = new E2("x"); $e->foo = 1;         // an engine class without the flag deprecates
echo "done\n";
