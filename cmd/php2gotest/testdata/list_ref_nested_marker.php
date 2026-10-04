<?php
// A nested by-reference destructuring boxes the inner array only to bind
// inside it: the temporary holding that box dies with the statement, so
// the inner array does not dump with `&` -- the elements bound by name do
// (list_reference_001, _009). An explicit `&` on the inner array itself is
// a real binding and keeps its marker.

$arr = [1, [2]];
list(&$a, list(&$b)) = $arr;
var_dump($arr);

$arr = [1, [2, [3]]];
[, [&$x, [&$y]]] = $arr;
$y = 30;
var_dump($arr);

$arr = [1, [2]];
[&$a, &$inner] = $arr;
var_dump($arr);

// In a foreach, the element a by-reference destructuring binds is released
// when the variable moves on and when the loop ends: only what a variable
// still holds keeps its marker (list_reference_004).
$c = [[1, 2], [3, 4]];
foreach ($c as [&$x, $y]) {
    $x++;
}
var_dump($c);
unset($x);
var_dump($c);
