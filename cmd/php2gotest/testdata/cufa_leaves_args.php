<?php
// call_user_func_array does not make its array's elements references: a
// by-reference parameter given a plain element warns and gets a reference to
// a copy, and a prefer-reference one (array_multisort) takes the value
// silently -- either way the caller's array is left as it was
// (call_user_func_array_prefer_ref). A written `...$a` does bind them.

function bump(&$x) { $x++; }

$args = [1];
call_user_func_array('bump', $args);
var_dump($args);

$args = [[3, 2, 1]];
call_user_func_array('array_multisort', $args);
var_dump($args);

$held = 5;
$args = [&$held];
call_user_func_array('bump', $args);
var_dump($held);

$spread = [1];
bump(...$spread);
var_dump($spread);

// call_user_func likewise hands a by-reference parameter a copy of what it
// was given, prefer-reference or not.
function push(&$a) { $a[] = 9; }
$x = [1];
call_user_func('push', $x);
var_dump($x);
$y = [3, 1, 2];
call_user_func('array_multisort', $y);
var_dump($y);
$z = [[1], [2]];
array_map(function (&$row) { $row[] = 0; }, $z);
var_dump($z);
