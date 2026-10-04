<?php
// Three rules about which class a name or a frame belongs to.
//
// Both A and B extends A declaring `private $x`: from outside, `$b->x` means
// B's own declaration and is an Error -- not a dynamic property -- while an
// ancestor's private alone stays invisible.
//
// An object bound to a closure with no scope gets `Closure` as the scope.
//
// A closure's frame is named by its scope, with the receiver only deciding
// the arrow.

class A
{
    private $x = 1;
    function inc() { return function () { return ++$this->x; }; }
    function mk() { return function () { echo (new Exception)->getTraceAsString(), "\n"; }; }
}
class B extends A { private $x = 10; }
class C extends A { }

$b = new B;
try {
    $b->x++;
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    var_dump($b->x);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
var_dump(isset($b->x));
$c = new C;
var_dump(@$c->x);

$inc = $b->inc();
var_dump($inc());
foreach ([$inc->bindTo($b, null), Closure::bind($inc, $b, null)] as $unscoped) {
    try {
        var_dump($unscoped());
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
var_dump($inc->bindTo($b)());

$f = function () {
    echo (new Exception)->getTraceAsString(), "\n";
    var_dump(static::class, get_class($this));
};
$f->bindTo(new C)();
$m = (new B)->mk();
$m();
Closure::bind($m, new B, "B")();
