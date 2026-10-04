<?php
// call_user_func_array's string keys name parameters, for a compiled
// function and a library function alike (bug79979); a positional entry
// after a named one is refused with the plain message, where a written
// `...$x` says `during unpacking` (named_params/call_user_func_array).

namespace Foo;

function f($a = 'a', $b = 'b', $c = 'c') { return "$a $b $c"; }

var_dump(call_user_func_array('Foo\f', ['A', 'c' => 'C']));
var_dump(\call_user_func_array('Foo\f', ['b' => 'B']));
var_dump(call_user_func_array('str_repeat', ['times' => 2, 'string' => 'xy']));
try {
    call_user_func_array('Foo\f', ['a' => 1, 2]);
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    \call_user_func_array('Foo\f', ['a' => 1, 2]);
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    f(...['a' => 1, 2]);
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
