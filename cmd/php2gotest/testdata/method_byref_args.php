<?php
// A method's by-reference parameter binds the caller's storage however the
// call is dispatched -- a variable, a new element `$a[]`, a nested element,
// from global scope, from inside the class, and a private method reached from
// its own scope -- while a by-value parameter handed `$a[]` is refused.

class X {
    function r(&$x) { $x = "ok"; }
    private function p(&$x) { $x = "private"; }
    function v($x) {}
    function run() {
        $ar = [];
        $this->r($ar[]);
        $n = 0;
        $this->p($n);
        var_dump($ar, $n);
        try { $this->v($ar[]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
    }
}
$v = 1; (new X)->r($v); var_dump($v);
$o = new X; $w = ["k" => 1]; $o->r($w["k"]); $o->r($w["new"]["deep"]); var_dump($w);
$o->run();
function v($x) { var_dump($x); }
class AA implements ArrayAccess { function offsetExists($o): bool { return true; } function offsetGet($o): mixed { echo "get\n"; return 1; } function offsetSet($o, $v): void {} function offsetUnset($o): void {} }
$f = "v";
$aa = new AA;
try { $f($aa[]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
$a = [];
try { $f($a[]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump($a);
class Kid extends X {}
(new Kid)->run();
// Dispatched by name at run time, or through call_user_func_array and an
// unpacked array of references: the parameter still binds.
class Y { function r(&$x) { $x++; } }
$yo = new Y; $m = "r"; $n = 1;
$yo->$m($n); var_dump($n);
call_user_func_array([$yo, 'r'], [&$n]); var_dump($n);
$refs = [&$n]; $yo->r(...$refs); var_dump($n);
