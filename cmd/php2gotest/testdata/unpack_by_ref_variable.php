<?php
// Unpacking a variable into by-reference parameters binds the variable's own
// elements: the callee's writes reach `$arr`, never a copy sharing its body,
// and positions the callee takes by value stay values.

function inc(&...$args) {
    foreach ($args as &$arg) {
        $arg++;
    }
}
function mixed($a, &$b, ...$rest) {
    $a = 'A'; $b = 'B'; $rest[0] = 'R';
}

$arr = [1, 2];
$keep = $arr;
$arr[] = 3;
$arr2 = $arr;
inc(...$arr);
var_dump($arr, $arr2);

$m = [1, 2, 3];
$copy = $m;
mixed(...$m);
var_dump($m, $copy);

function gen() { yield 1; yield 2; }
inc(...gen());
echo "done\n";

function inc2(&...$args) { $args[0]++; }
$arr = [1, 2];
$keep = $arr;
inc($arr[0], $arr[1]);
var_dump($arr, $keep);
$b = [1, 2];
inc2($b[0]);
var_dump($b);
