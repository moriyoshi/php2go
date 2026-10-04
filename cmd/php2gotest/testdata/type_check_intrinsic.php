<?php
// The whole is_* family is ONE operation: the engine compiles each to
// ZEND_TYPE_CHECK with a type mask, so what differs between them is the mask
// and nothing else. is_numeric is deliberately not one of them -- it asks
// whether a string would read as a number, not what type the value is.
$values = [1, 1.5, "s", "12", [1], null, true, false, new stdClass];
foreach ($values as $v) {
    var_dump(is_int($v), is_float($v), is_string($v), is_array($v),
        is_object($v), is_null($v), is_bool($v), is_scalar($v), is_numeric($v));
}
// An undefined variable is read the loud way: the warning comes first and the
// answer follows.
var_dump(is_null($undefined));
var_dump(is_int($alsoUndefined));
var_dump(is_array($nope));
// A reference and a property reach the same operation.
$a = 1; $r = &$a;
var_dump(is_int($r));
class C { public $p = "x"; public $u; }
$o = new C;
var_dump(is_string($o->p), is_null($o->u), is_int($o->missing));
$arr = ["k" => 2.5];
var_dump(is_float($arr["k"]), is_null($arr["absent"]));

// The dynamic call is the family's other entry point, and it must agree with
// the opcode: `$f = 'is_int'; $f($v)` cannot reach ZEND_TYPE_CHECK, so it goes
// through the library table instead. Both sides call php.TypeCheck.
foreach (["is_int", "is_integer", "is_long", "is_float", "is_double", "is_string",
          "is_array", "is_object", "is_null", "is_bool", "is_scalar", "is_numeric"] as $f) {
    echo $f, ":";
    foreach ([1, 1.5, "2", [1], null, true, new stdClass] as $v) {
        echo $f($v) ? "1" : "0";
    }
    echo "\n";
}
