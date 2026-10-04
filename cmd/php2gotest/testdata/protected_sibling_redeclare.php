<?php
// A protected property two siblings redeclare from a common ancestor is the
// ancestor's: each sibling's methods reach the other's copy, for reads,
// writes and isset. A class outside the family still cannot.

abstract class P { protected $foo; }
class C1 extends P { protected $foo = 1; }
class C2 extends P
{
    protected $foo = 2;
    static function peek($c) { return $c->foo; }
    static function poke($c) { $c->foo = "poked"; return isset($c->foo); }
}
class Outsider { static function peek($c) { return $c->foo; } }

var_dump(C2::peek(new C2), C2::peek(new C1));
$one = new C1;
var_dump(C2::poke($one), C2::peek($one));
try {
    Outsider::peek(new C1);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
