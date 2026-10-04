<?php
class C {
    private $items = [1, 2, 3];
    public function each() { foreach ($this->items as $i) { yield $i * 10; } }
}
$c = new C();
foreach ($c->each() as $v) { echo $v, " "; }
echo "\n";
$gen = function () { yield "a"; yield "b"; };
foreach ($gen() as $v) { echo $v, " "; }
echo "\n";
