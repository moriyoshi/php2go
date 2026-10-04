<?php
// A closure's `static` variables belong to the closure object, not to the
// literal: two closures made from one literal count separately, generators
// made by calling one closure share its statics (bug64979), and bindTo,
// Closure::bind and clone start from a copy that then counts on its own.
// var_dump shows the object's own values.
function side($v) { echo "init $v\n"; return $v; }
function mkd() { return function() { static $n = side(5); return ++$n; }; }
$p = mkd(); $q = mkd();
echo $p(), $p(), $q(), "\n";
function mk() { return function() { static $n = 5; static $arr = []; $arr[] = ++$n; return $arr; }; }
$a = mk(); $b = mk();
var_dump($a);
echo json_encode($a()), json_encode($a()), json_encode($b()), "\n";
var_dump($a);
$c = $a->bindTo(null); echo json_encode($c()), json_encode($a()), "\n";
$d = clone $a; echo json_encode($d()), json_encode($a()), "\n";
$e = Closure::bind($a, null, null); echo json_encode($e()), json_encode($a()), "\n";
function mkgen() { return function() { static $g = 100; while (true) yield ++$g; }; }
$x = mkgen(); $y = mkgen();
$g1 = $x(); $g2 = $x(); $g3 = $y();
echo $g1->current(), " ", $g2->current(), " ", $g3->current(), "\n";
$g1->next(); echo $g1->current(), " ", $g2->current(), "\n";
// A capture and a static side by side: the capture is not a static.
$u = 7;
$h = function() use (&$u) { static $q = 1; return $q++; };
echo $h(), $h(), "\n";
var_dump($h);
$fs = [];
for ($i = 0; $i < 3; $i++) { $fs[] = function() { static $k = 10; return ++$k; }; }
foreach ($fs as $f) echo $f(), $f(), " ";
echo "\n";
// A generator closure shows its statics too, once its generator is gone.
$w = function() { static $once = 100; yield ++$once; };
var_dump($w);
foreach ($w() as $v) echo $v, "\n";
var_dump($w);
