<?php
// A trace records a by-reference argument by the value it held when the
// trace was taken, not by what the reference holds when the trace is read
// (gh16188). A by-value argument is unaffected either way.

function f(&$x, $y) {
    $e = new Exception("in f");
    $x = 'changed later';
    $y = 'also changed';
    return $e;
}
$v = 'first';
$w = 'second';
$e = f($v, $w);
var_dump($e->getTrace()[0]['args'], $v);
echo $e->getTraceAsString(), "\n";

function g(&$arr) {
    $t = debug_backtrace();
    $arr[] = 'pushed';
    return $t;
}
$list = [1];
var_dump(g($list)[0]['args'], $list);
