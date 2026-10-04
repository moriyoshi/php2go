<?php
// `$$n` naming `this` reads `$this` where one is bound -- a method, an arrow
// function or closure inside it -- though `$this` is not in the symbol
// table: isset($$n) is false even there, and in a static method or at file
// scope the read is an undefined variable (arrow_functions/005).
class T {
    public $p = 1;
    function m() {
        $n = "this";
        var_dump($$n);
        var_dump(isset($$n));
        $f = fn() => $$n;
        var_dump($f());
        $g = function () use ($n) { return $$n; };
        var_dump($g());
        var_dump(${"th" . "is"}->p);
    }
    static function s() {
        $n = "this";
        var_dump($$n);
        var_dump(isset($$n));
    }
}
(new T)->m();
T::s();
$n = "this";
var_dump(isset($$n));
var_dump($$n);
