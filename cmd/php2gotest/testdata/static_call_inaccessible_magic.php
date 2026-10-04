<?php
// A static-syntax call to a method the calling scope cannot reach falls to
// `__call` when a compatible `$this` is in scope, and to `__callStatic`
// only without one -- the same order a missing method gets.

class A {
    public function __call($m, $a) { echo "__call $m ", static::class, "\n"; }
    public static function __callStatic($m, $a) { echo "__callStatic $m ", static::class, "\n"; }
    private function test() { echo "private\n"; }
}
class B extends A {
    public function viaParent() { parent::test(); parent::nope(); }
    public function viaName() { A::test(); }
    public static function fromStatic() { parent::test(); }
}
class C {
    public function other() { A::test(); }
}
$b = new B;
$b->viaParent();
$b->viaName();
B::fromStatic();
(new C)->other();
