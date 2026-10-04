<?php
// A closure made from a method keeps that method's class: another class's
// object, a new scope, and unbinding $this are refused with a warning and
// null; a static method's closure takes no instance.
class A {
    public function method() { var_dump(get_class($this)); }
    public static function s() { echo "s\n"; }
}
class A2 extends A {}
class B {}
$fn = Closure::fromCallable([new A, 'method']);
var_dump($fn->call(new B));
var_dump($fn->bindTo(new B));
var_dump($fn->bindTo(new A2) !== null);
var_dump($fn->call(new A2));
var_dump($fn->bindTo(new A, 'B'));
var_dump($fn->bindTo(null));
$s = Closure::fromCallable(['A', 's']);
var_dump($s->bindTo(new A));
$fn();
