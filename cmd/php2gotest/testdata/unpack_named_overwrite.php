<?php
// A named argument after an unpack takes only a position the unpack left
// free: naming one it already filled is `overwrites previous argument`,
// whether the unpack filled it by position or by string key.

function test2($a, $b, $c = 3, $d = 4) { return "$a $b $c $d"; }
var_dump(test2(...[1, 2], d: 40));
var_dump(test2(...['b' => 2, 'a' => 1], d: 40));
foreach ([
    fn() => test2(...[1, 2], b: 20),
    fn() => test2(...[1, 'b' => 2], b: 20),
    fn() => test2(...[1], a: 10),
] as $f) {
    try {
        var_dump($f());
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
