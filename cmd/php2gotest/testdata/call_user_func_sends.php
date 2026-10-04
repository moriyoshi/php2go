<?php
// `call_user_func` sends its arguments with an opcode whose passing mode the
// engine settles at RUN time, because the callee is not known at compile time.
function byval($x) { $x = 99; return $x; }
function two($a, $b) { return $a . "-" . $b; }
$a = 1;
var_dump(call_user_func("byval", $a), $a);
var_dump(call_user_func("two", "x", "y"));
var_dump(call_user_func("byval", 5));
class C {
    public function m($v) { return "m:$v"; }
    public static function s($v) { return "s:$v"; }
}
$o = new C;
var_dump(call_user_func([$o, "m"], 1));
var_dump(call_user_func(["C", "s"], 2));
var_dump(call_user_func("C::s", 3));
echo "done\n";
