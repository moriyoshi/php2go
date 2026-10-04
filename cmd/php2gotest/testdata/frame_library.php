<?php
// The frame readers answer about the **caller's** frame. An engine function's
// own frame is on the stack in the interpreter and absent in compiled code, so
// neither reader can be written against a fixed offset from the top -- what
// they drop is the trailing internal frames, however many that is.
function f($p, $q) {
    var_dump(func_num_args(), func_get_args(), func_get_arg(1));
    try { func_get_arg(9); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
    try { func_get_arg(-1); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
}
f(1, 2, 3);
f();

function outerframe($x) { innerframe($x + 1); }
function innerframe($y) { debug_print_backtrace(); }
outerframe(1);

// An engine function that calls back is a frame of its own, and it is **not**
// trailing -- so the callback sees it and it must survive.
$seen = array_reduce([1], function ($carry, $v) { debug_print_backtrace(); return $v; }, 0);

class C {
    public function m() { debug_print_backtrace(); return func_num_args(); }
    public static function s() { debug_print_backtrace(); }
}
var_dump((new C)->m(7, 8));
C::s();

function g() { return func_get_args(); }
var_dump(g(), g(null), g([1], "x"));
