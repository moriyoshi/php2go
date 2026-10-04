<?php
class foo {
    public $x = [1, 2];
    public function b() { return $this->x; }
}
$f = new foo;
$f->b()[0] = 99;
var_dump($f->x);
$f->b()[0] += 5;
var_dump($f->x);
unset($f->b()[0]);
var_dump($f->x);
$f->b()[] = 3;
var_dump($f->x);
echo "still running\n";

function g() { return [1, 2]; }
g()[0] = 7;
echo "after g\n";
