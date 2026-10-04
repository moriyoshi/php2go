<?php
// `++` and `--` on an array element that is a typed property's reference
// refuse to run off the integer range with the step's own wording, and the
// property keeps its value -- including at PHP_INT_MIN, where the float the
// step produces would be accepted by a plain assignment.

class A { public int $n = PHP_INT_MIN; public int|float $w = PHP_INT_MAX; }
$a = new A;
$arr = [&$a->n, &$a->w];
foreach ([fn() => $arr[0]--, fn() => --$arr[0]] as $f) {
    try {
        $f();
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
var_dump($a->n);
$a->n = PHP_INT_MAX;
$holder = new stdClass;
$holder->list = [&$a->n];
try {
    $holder->list[0]++;
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump($a->n);
$arr[1]++;
var_dump($a->w);
$arr["0"] = 5;
$arr["0"]--;
var_dump($a->n);
