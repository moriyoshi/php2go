<?php
class A { const K = 5; public static $s = 7; public static function m() { return "sm"; }
          public function im() { return "im"; } }
$n = "A";
$o = new $n();
var_dump($o instanceof A);
echo get_class($o), "\n";
echo $n::K, "\n";
echo $n::$s, "\n";
echo $n::m(), "\n";
echo A::class, "\n";
$obj = new A();
echo $obj::class, "\n";
try { $bad = "Nope"; new $bad(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
