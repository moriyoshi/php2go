<?php
// `$f()` on a callable naming a class -- "C::m" or ["C", "m"] -- must reach a
// static method: a non-static one, and the `__call` a missing name would
// reach, are refused even with a compatible `$this`, which call_user_func
// uses instead. `__callStatic` answers.
class Foo {
    function inst() { echo "inst\n"; }
    static function st() { echo "st\n"; }
    function t() {
        foreach ([['Foo', 'inst'], 'Foo::inst', ['Foo', 'st'], 'Foo::st', ['Foo', 'nope'], 'Foo::nope'] as $m) {
            try { $m(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
            try { call_user_func($m); } catch (Error $e) { echo "cuf ", get_class($e), ": ", $e->getMessage(), "\n"; }
        }
    }
    function __call($n, $a) { echo "__call $n\n"; }
}
(new Foo)->t();
class Bar { static function __callStatic($n, $a) { echo "__callStatic $n\n"; } function inst() {} function t() { $m = ['Bar', 'nope']; $m(); $m = 'Bar::nope'; $m(); } }
(new Bar)->t();
