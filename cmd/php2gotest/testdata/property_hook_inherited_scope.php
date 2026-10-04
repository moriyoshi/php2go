<?php
class A { public $p = 1; }
class B extends A { public $p = 2 { get => parent::$p::get() * 2; } }
class C extends B { public $p = 3; }
class D extends C { public $p = 4; }
class E extends B { public $p = 5 { get => parent::$p::get() + 100; } }
class F extends E { }
foreach ([new B, new C, new D, new E, new F] as $o) { echo get_class($o), " ", $o->p, "\n"; }
// set hooks
class S1 { public $v = 0; }
class S2 extends S1 { public $v = 0 { set { parent::$v::set($value * 10); } } }
class S3 extends S2 { public $v = 7; }
$s = new S3; $s->v = 2; echo "S3 ", $s->v, "\n";
$s = new S2; $s->v = 3; echo "S2 ", $s->v, "\n";
// get-only override, set inherited
class G1 { public $w = 1; }
class G2 extends G1 { public $w = 1 { get => parent::$w::get() + 1; set { parent::$w::set($value + 1); } } }
class G3 extends G2 { public $w = 1 { get => parent::$w::get() * 3; } }
$g = new G3; $g->w = 5; echo "G3 ", $g->w, "\n";
// private access in inherited hook
class P1 { private $secret = 'P1'; public $x { get => $this->secret; } }
class P2 extends P1 { private $secret = 'P2'; public $x; }
echo "P2 ", (new P2)->x, "\n";
// abstract / interface hook
interface I { public $q { get; } }
abstract class J implements I { public $q = 9 { get => $this->q * 2; } }
class K extends J { public $q = 4; }
echo "K ", (new K)->q, "\n";
// set inherited by a class linked at run time (interface above it)
interface IS { public $r { set; } }
abstract class JS implements IS { public $r = 0 { set { $this->r = $value * 7; } } }
class KS extends JS { public $r = 1; }
$k = new KS; $k->r = 3; echo "KS ", $k->r, "\n";
// get overridden, set inherited, run-time linked
class KG extends JS { public $r = 1 { get => $this->r + 1000; } }
$k = new KG; $k->r = 2; echo "KG ", $k->r, "\n";
// an abstract hook is not inherited by a plain redeclaration
abstract class AP { abstract public $z { get; } }
class AC extends AP { public $z = 11; }
echo "AC ", (new AC)->z, "\n";
interface AI { public $y { set; } }
class AJ implements AI { public $y = 0; }
$j = new AJ; $j->y = 9; echo "AJ ", $j->y, "\n";
