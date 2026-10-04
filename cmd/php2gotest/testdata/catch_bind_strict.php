<?php
// A caught exception is bound by a strict assignment, even in a weak-mode
// file: a catch variable that is a reference held by a `string` property is a
// TypeError, where an ordinary assignment would have called __toString.

class Test { public string $s = "str"; public int $i = 1; }
$t = new Test;
$ref = &$t->s;
try {
    try {
        throw new Exception("ex");
    } catch (Exception $ref) {
        echo "unreachable\n";
    }
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump($t->s);
$other = &$t->s;
$other = new class { function __toString(): string { return "converted"; } };
var_dump($t->s);
