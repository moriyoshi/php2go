<?php
function g() {
    yield 1;
    yield 2;
    return 9;
}
$it = g();
foreach ($it as $v) { echo $v, "\n"; }
echo $it->getReturn(), "\n";

// Keys are the generator's own, counting from zero across yields.
function kv() { yield "a" => 1; yield "b" => 2; }
foreach (kv() as $k => $v) { echo $k, "=", $v, "\n"; }

// A generator with state that must survive each suspension.
function counter($n) {
    $total = 0;
    for ($i = 0; $i < $n; $i++) { $total = $total + $i; yield $total; }
    return $total;
}
$c = counter(4);
foreach ($c as $t) { echo $t, " "; }
echo "\n", $c->getReturn(), "\n";

// The driver surface directly.
$m = g();
echo $m->current(), "\n";
$m->next();
echo $m->current(), "\n";
var_dump($m->valid());
$m->next();
var_dump($m->valid());
