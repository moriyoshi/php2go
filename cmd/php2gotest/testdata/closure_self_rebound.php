<?php
// `self` and `parent` in a closure mean the scope the closure is bound to
// when it runs, not the class it was written in.

class P { const C = 'P::C'; public static function who() { return 'P::who'; } }
class A {
    const C = 'A::C';
    private static $s = 'A::$s';
    public function f() {
        return function () {
            return [self::C, self::class, static::class];
        };
    }
    public function g() {
        return function () { return parent::C . ' ' . parent::who(); };
    }
}
class Q extends P {}
class B extends P {
    const C = 'B::C';
    private static $s = 'B::$s';
    public function h() {
        return function () { return self::$s . ' ' . get_class(new self); };
    }
}

$f = (new A)->f();
var_dump($f());
var_dump($f->bindTo(new B, 'B')());
var_dump(Closure::bind($f, null, 'Q')());
$g = (new A)->g();
var_dump(Closure::bind($g, null, 'Q')());
$h = (new B)->h();
var_dump($h());
