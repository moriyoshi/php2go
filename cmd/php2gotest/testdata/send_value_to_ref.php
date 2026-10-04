<?php
// A value sent where a declared function takes a reference is an Error at the
// send, before any later argument is evaluated -- the engine compiles `foo(1)`
// against `foo(&$v)` to SEND_VAL_EX, and that opcode throws.

function foo(&$var) { $var = "written"; }
function two($a, &$b) { }
function variadic(&...$rest) { }
function side($x) { echo "side($x)\n"; return $x; }

try {
    foo(1);
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
try {
    two(side(1), 2 + side(3), side(4));
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    variadic($ok, "literal");
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
var_dump($ok ?? "unset");
try {
    variadic("first");
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
$v = 1;
foo($v);
var_dump($v);
echo "Done\n";

// A static method named literally is refused the same way.
class Holder
{
    public static function usesRef(&$x) { $x = "modified"; }
}
try {
    Holder::usesRef(match (true) { true => 1 });
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
$fine = 1;
Holder::usesRef($fine);
var_dump($fine);
