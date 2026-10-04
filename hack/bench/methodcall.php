<?php
class Foo {
    const TEST = 0;
    public $x = 0;
    function f() {}
    static function g() {}
    function calls($n) {
        for ($i = 0; $i < $n; ++$i) { $this->f(); }
        for ($i = 0; $i < $n; ++$i) { self::g(); }
        for ($i = 0; $i < $n; ++$i) { $x = Foo::TEST; }
        for ($i = 0; $i < $n; ++$i) { $y = isset($this->x); }
    }
}
$t = hrtime(true); (new Foo)->calls(2000000); echo "methodcall ", round((hrtime(true)-$t)/1e9, 3), "\n";
