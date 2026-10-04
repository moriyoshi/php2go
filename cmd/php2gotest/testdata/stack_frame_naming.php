<?php
function tr() { $e = new Exception("t"); echo $e->getTraceAsString(), "\n---\n"; }
class A { public function who() { tr(); } public static function st() { tr(); } }
class B extends A { public function go() { parent::who(); } }
class C extends B {}
(new C)->go();
(new C)->who();
C::st();
class D extends Exception {}
$d = new D("m");
echo $d->getTraceAsString(), "\n---\n";
class E { public function __construct() { tr(); } }
class F extends E {}
new F;
class G { public function __call($n,$a){ tr(); } }
(new G)->nope();
class H { public function __get($n){ tr(); return 1; } }
$h = new H; $x = $h->zz;
// A user exception subclass captures its trace at `new`, before its own
// constructor frame exists.
class K extends Exception {}
function mk() { return new K("k"); }
echo mk()->getTraceAsString(), "\n---\n";
