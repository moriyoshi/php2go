<?php
// get_object_vars() from a class whose private property a subclass redeclares
// shows the scope's own private value under that name; from the subclass, or
// from outside, the subclass's is the one listed.

class A {
    private $prop = 'A::$prop';
    private $only = 'A::$only';
    public function fromA() { return get_object_vars($this); }
}
class B extends A {
    protected $prop = 'B::$prop';
    public $pub = 'B::$pub';
    public function fromB() { return get_object_vars($this); }
}
$b = new B;
var_dump($b->fromA(), $b->fromB(), get_object_vars($b));
class C extends A { public $prop = 'C::$prop'; }
var_dump((new C)->fromA());
