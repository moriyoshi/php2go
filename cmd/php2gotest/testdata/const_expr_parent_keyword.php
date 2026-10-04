<?php
// `parent::C` in a constant expression is resolved when the expression is
// evaluated: with no parent it is `Cannot access "parent" when current class
// scope has no parent` (invalid_parent_const_ref_leak), not a class named
// "". With one, it reads the parent's constant; `self::` reads the class's.

class A {
    const B = parent::C;
    const S = self::T;
    const T = 't';
}
class P {
    const C = 'from P';
}
class Q extends P {
    const D = parent::C . '!';
    public $prop = parent::C;
}
class R {
    public $bad = parent::C;
}

foreach ([fn() => A::B, fn() => A::S, fn() => Q::D, fn() => (new Q)->prop, fn() => (new R)->bad] as $f) {
    try {
        var_dump($f());
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
