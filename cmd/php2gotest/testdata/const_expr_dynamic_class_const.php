<?php
// `C::{expr}` in a constant expression: the member expression is evaluated
// first -- an undefined constant there is reported before the class is looked
// up -- and must be a string; `{'class'}` is not the class's name here, as it
// is at run time, but an undefined constant (gh10486_2).

class C {
    const A = 1;
    const B = C::{K2};
}
const K = "A";
const K2 = "A";
const V = C::{K};
var_dump(V, C::B);
function f($x = C::{K}) { return $x; }
var_dump(f());
function fc($y = C::{'class'}) { return $y; }
try {
    fc();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
function g($x = C::{1}) { return $x; }
try {
    g();
} catch (TypeError $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
function h($x = Missing::{Nope}) { return $x; }
try {
    h();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
function i($x = Missing::{K}) { return $x; }
try {
    i();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
