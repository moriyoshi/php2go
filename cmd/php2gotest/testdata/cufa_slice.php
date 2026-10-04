<?php
// `call_user_func_array($f, array_slice($a, N, $len))` compiles to one
// SEND_ARRAY carrying the offset and the length: the slice's values are sent
// positionally whatever their keys, and a bad length is array_slice's refusal.

function show(...$a) { var_dump($a); }

$array = [1, 2, 3];
try {
    $len = [];
    call_user_func_array('show', array_slice($array, 0, $len));
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
$len = 2.0;
call_user_func_array('show', array_slice($array, 0, $len));
$len = null;
call_user_func_array('show', array_slice($array, 1, $len));
call_user_func_array('show', array_slice($array, 1, -1));
call_user_func_array('show', array_slice(["a" => 1, "b" => 2, 3], 0, 3));
call_user_func_array('show', array_slice($array, 5, 2));
