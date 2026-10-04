<?php
class A {
    public $q = 1;
    public function foo() { $g = function () { return "m"; }; return $g(); }
    public static function bar() { $h = function () { return "s"; }; return $h(); }
}
$a = new A();
echo $a->foo(), A::bar(), "\n";
function rec($n) { if ($n <= 0) { return "done"; } return rec($n - 1); }
echo rec(3), "\n";
$viaName = "rec";
echo $viaName(2), "\n";

// A closure declared in a class takes that class's *scope*, not just its
// receiver: it can reach `private` members through $this.
class Scoped {
    private $secret = "inner";
    public function make() { return function () { return $this->secret; }; }
}
$s = new Scoped();
$f = $s->make();
echo $f(), "\n";
