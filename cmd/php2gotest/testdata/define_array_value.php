<?php
// define() stores an array by value: references in it are resolved to the
// values they hold at the time, so a later write through the reference does
// not reach the constant; a recursive array is refused.

$x = 1;
$y = [&$x, [&$x]];
define('QUX', $y);
$x = 7;
var_dump(QUX, $y[0]);

$recursive = [];
$recursive[0] = &$recursive;
try {
    define('RECURSION', $recursive);
} catch (ValueError $e) {
    echo $e->getMessage(), "\n";
}
var_dump(defined('RECURSION'));
