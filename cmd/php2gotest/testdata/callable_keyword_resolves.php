<?php
// A keyword in a callable -- `self`, `parent`, `static` -- is deprecated only
// when it resolves: from a class with no parent, `['parent', 'm']` is simply
// not callable, and with no class scope neither is `['self', 'm']`
// (traits/bug76773-deprecated).

class P { static function m() { return "P::m"; } }
class C extends P {
    static function t() {
        var_dump(is_callable(['parent', 'm']), call_user_func(['parent', 'm']));
    }
}
class N {
    static function m() { return "N::m"; }
    static function t() {
        var_dump(is_callable(['parent', 'm']));
        var_dump(is_callable(['self', 'm']), call_user_func(['self', 'm']));
        try {
            call_user_func(['parent', 'm']);
        } catch (Error $e) {
            echo get_class($e), ": ", $e->getMessage(), "\n";
        }
    }
}
C::t();
N::t();
var_dump(is_callable(['self', 'm']), is_callable('static::m'));
