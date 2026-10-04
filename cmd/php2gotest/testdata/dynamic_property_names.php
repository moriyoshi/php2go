<?php
// A computed property name: the binding takes a Go string, so the value is
// converted at run time.
class A { public $p = 1; public $q = 2; }
$o = new A;
$name = "p";
var_dump($o->$name);
$o->$name = 9;
var_dump($o->p);
$m = "q";
var_dump(isset($o->$m), $o->{$m});
unset($o->$m);
var_dump($o);
// `$this` as the receiver of isset/unset is an UNUSED op1 in the engine, not a
// missing operand -- and it is a parameter here, not a local slot.
class B {
    public $x = 1;
    private $y = 2;
    public function has($n) { return isset($this->$n); }
    public function drop($n) { unset($this->$n); }
    public function isSelf() { return $this instanceof B; }
    public function myClass() { return $this::class; }
}
$b = new B;
var_dump($b->has("x"), $b->has("nope"), $b->isSelf(), $b->myClass());
$b->drop("x");
var_dump($b);
echo "done\n";
// `isset($this)` is a value-position `$this`, which renders as the receiver
// parameter and not as a local slot.
class D { public function selfSet() { return isset($this); } }
var_dump((new D)->selfSet());
// In a STATIC method there is no receiver at all, and `isset($this)` there is
// legal PHP answering false.
class E { public static function noSelf() { return isset($this); } }
var_dump(E::noSelf());
echo "end\n";
