<?php
// array_walk is the by-reference member of the callback family: the callback's
// first parameter is bound to the element's own storage, so writing to it
// writes into the array. It also walks an **object**, over the raw property
// table -- every property, whatever the calling scope can see.
$a = ["x" => 1, "y" => 2];
array_walk($a, function (&$v, $k) { $v = "$k=$v"; });
var_dump($a);
$b = [1, 2, 3];
array_walk($b, function (&$v, $k, $extra) { $v *= $extra; }, 10);
var_dump($b);
// Without the reference the array is untouched.
$c = [1, 2];
array_walk($c, function ($v) { $v = 99; });
var_dump($c);
class P { public $pub = 1; protected $prot = 2; private $priv = 3; }
$o = new P;
array_walk($o, function (&$v, $k) { $v = strtoupper($k); });
print_r($o);
// An unusable callback is refused before anything is written.
$d = [1, 2];
try { array_walk($d, "nosuchfunction"); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
try { array_walk($d, 42); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
var_dump($d);
try { array_walk($notAnArray, fn(&$v) => $v); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
