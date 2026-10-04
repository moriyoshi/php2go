<?php
class A {
    public $tag = "A";
    function make() { return new self; }
    static function smake() { return new static; }
}
class B extends A {
    public $tag = "B";
    function makeParent() { return new parent; }
}
$a = new A; $b = new B;
var_dump($a->make()->tag, $b->make()->tag, $b->makeParent()->tag);
var_dump(A::smake()->tag, B::smake()->tag);
// The keywords are case-insensitive, which is how the original bug was found.
class C { public $t = "C"; function m() { return new sELF; } }
var_dump((new C)->m()->t);
echo "done\n";
