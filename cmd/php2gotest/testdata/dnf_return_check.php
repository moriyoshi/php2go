<?php
// A DNF type accepts an object satisfying every part of some term, and
// refuses any other object -- or any non-object -- as a return and as a
// parameter.

interface W {}
interface X {}
interface Y {}
interface Z {}
class A implements X, Y {}
class B implements W, Z {}
class C implements X {}

function pick($v): (X&Y)|(W&Z) { return $v; }
function take((X&Y)|(W&Z)|null $v) { return $v === null ? "null" : get_class($v); }
foreach ([new A, new B, new C, 1, "s"] as $v) {
    try {
        echo get_class(pick($v)), "\n";
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
foreach ([new A, null, new C] as $v) {
    try {
        echo take($v), "\n";
    } catch (TypeError $e) {
        echo strstr($e->getMessage(), ', called in', true), "\n";
    }
}
