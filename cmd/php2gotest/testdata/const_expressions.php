<?php
const G = 5;
class C {
    const A = 1;
    const B = self::A + 1;
    const D = G * 2;
    const E = "x" . "y";
    public $p = self::A;
    public $q = G;
    public $r = 3;
}
$c = new C();
echo C::A, C::B, C::D, C::E, "\n";
var_dump($c->p, $c->q, $c->r);
