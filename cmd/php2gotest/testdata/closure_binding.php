<?php
class A { private $p = 7; }
$f = function() { return $this->p ?? "none"; };
$b = Closure::bind($f, new A, A::class);
var_dump($b());
$c = $f->bindTo(new A, A::class);
var_dump($c());
$d = $f->bindTo(new A);
var_dump($d());
var_dump($f->call(new A));
$g = function($x) { return $x * 2; };
var_dump($g->__invoke(4));
function plain($x) { return $x + 1; }
$h = Closure::fromCallable("plain");
var_dump($h(1));
$n = 1;
$k = function() use (&$n) { $n++; return $n; };
$k2 = $k->bindTo(null);
var_dump($k(), $k2(), $n);
echo "done\n";
