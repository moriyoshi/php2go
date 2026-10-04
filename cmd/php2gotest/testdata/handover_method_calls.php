<?php
// A fresh object passed to a method, a static method or a closure whose
// body is known dies among that callee's variables when it returns --
// parameters first, then locals -- as a function's argument does.
class D {
    function __construct(public string $n) {}
    function __destruct() { echo "-{$this->n} "; }
}

class C {
    function m($a, $b) { $x = new D('x'); echo "m "; }
    // `$this` may be a Sub, whose override owns its arguments too.
    function self_call() { $this->m(new D('ta'), new D('tb')); echo "self "; }
}
class Sub extends C {
    function m($a, $b) { echo "sub "; }
}
class S {
    static function s($a) { echo "s "; }
}

// A subclass keeps its argument: whatever the parent owns, the call made
// through the parent's type must not free it.
class A { function m($a) { echo "A "; } }
class B extends A {
    public static $keep = [];
    function m($a) { self::$keep[] = $a; echo "B "; }
}
class Holder { function run(A $x) { $x->m(new D('kept')); echo "run "; } }

function methods() {
    $c = new C;
    $c->m(new D('a'), new D('b'));
    echo "| method\n";
    $c->self_call();
    echo "| this\n";
    $s = new Sub;
    $s->m(new D('sa'), new D('sb'));
    echo "| subclass\n";
    $s->self_call();
    echo "| this in subclass\n";
    S::s(new D('st'));
    echo "| static\n";
    $b = new B;
    $b->m(new D('bk'));
    echo "| keeps\n";
    (new Holder)->run(new B);
    echo "| keeps via parent\n";
}
methods();

function closures() {
    $f = function ($a) { $y = new D('cy'); echo "cl "; };
    $f(new D('ca'));
    echo "| closure\n";
    $g = fn($a) => print("arrow ");
    $g(new D('ar'));
    echo "| arrow\n";
    $k = function ($a) { B::$keep[] = $a; echo "kc "; };
    $k(new D('kc'));
    echo "| closure keeps\n";
}
closures();
echo "| end\n";
