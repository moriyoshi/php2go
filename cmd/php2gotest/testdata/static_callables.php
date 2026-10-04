<?php
class S {
    public static function m() { echo "S::m\n"; }
    public function inst() { echo "inst\n"; }
    public static function __callStatic($n, $a) { echo "cs($n)\n"; }
}
$t = "S::m"; $t();
$u = ["S", "m"]; $u();
$v = "S::nope"; $v();
$w = ["S", "NOPE"]; $w();
call_user_func("S::m");
call_user_func(["S", "m"]);
class T { public function method() { return "x"; } }
$c = "T::method";
try { $c(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
$d = ["T", "method"];
try { $d(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
$obj = new T; $e2 = [$obj, "method"]; var_dump($e2());
echo "done\n";
