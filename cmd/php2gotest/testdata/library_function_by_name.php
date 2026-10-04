<?php
// A library function the program only names as a string is reachable by a
// dynamic call, a callable string, and call_user_func.
function mine() {}

$f = 'ucfirst';
var_dump($f('hello'));
$g = 'lcfirst';
var_dump(call_user_func($g, 'WORLD'));
var_dump(array_map('ucwords', ['a b', 'c d']));
var_dump(is_callable('strrev'), function_exists('strrev'));
$h = 'str_pad';
var_dump($h('x', 3, '-', STR_PAD_BOTH));
// The program's own functions are the only user functions.
var_dump(get_defined_functions()['user']);
// An argument error raised inside the library function names it.
try {
    $f();
} catch (ArgumentCountError $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    $f([]);
} catch (TypeError $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
// A null for a scalar parameter deprecates at the caller's line.
var_dump(ucfirst(null));
var_dump($f(null));
var_dump(str_pad('a', null));
$name = 'nl2br';
echo $name("a\nb"), "\n";
