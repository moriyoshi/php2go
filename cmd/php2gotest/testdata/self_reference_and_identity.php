<?php
// A variable a reference was taken to keeps its box when it is assigned:
// `$a = [&$a]` makes an array containing itself, and a variable handed by
// reference to a function that keeps the reference is written through
// afterwards. `===` on a recursive structure is `Nesting level too deep`;
// on the same array it is true without looking inside. The precision-loss
// deprecation spells the float at full precision.

$a = [&$a];
var_dump(count($a[0]), count($a[0][0]));
try {
    var_dump($a === [[]]);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
var_dump($a === $a);

function keep(&$ref) { static $kept; $kept = &$ref; return function () use (&$kept) { return $kept; }; }
function viaKeep() {
    $v = [1];
    $read = keep($v);
    $v = [2, 3];
    return $read();
}
var_dump(viaKeep());

$s = "abcdef";
var_dump(isset($s[M_PI]), $s[M_PI]);
