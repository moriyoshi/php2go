<?php
// A default that is a constant expression is type-checked once evaluated,
// in the same words a passed argument is -- only a literal `null` default
// makes a parameter implicitly nullable, not a constant that is null. A
// literal default is not checked again.

const NULL_VAL = null;
const STRING_VAL = "test";
const NUM_STRING = "42";

function f(int $a = NULL_VAL) { var_dump($a); }
function g(?int $a = NULL_VAL) { var_dump($a); }
function h(int $a = STRING_VAL) { var_dump($a); }
function k(int $a = NUM_STRING) { var_dump($a); }
function m(int $a = null) { var_dump($a); }

foreach (['f', 'g', 'h', 'k', 'm'] as $fn) {
    try { $fn(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
}
f(5);
