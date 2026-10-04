<?php
// A value sent to a by-reference parameter of a callee known only at run
// time -- `$f(1)`, `(expr)($a?->b)` -- is an Error at the send, naming the
// function as declared, a method by its declaring class and a closure by its
// own name. A variable is sent as one and binds (nullsafe_operator/016).

function SetIt(&$ref, $v = 1) { $ref = $v; }
class P { static function s(&$r) { $r = "s"; } function m(&$r) { $r = "m"; } }
class C extends P {}

$tests = ["setit", function (&$z) {}, fn(&$y) => 1, [new C, "m"], "C::s", ["C", "s"], "strlen"];
foreach ($tests as $f) {
    try {
        var_dump($f($x = "abc"));
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
$f = "SetIt";
$v = 0;
$f($v, 7);
var_dump($v);
try {
    $f($v, $w = 2);
    var_dump($v);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
$o = null;
try {
    (strrev("tItes"))($o?->p, 1);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
