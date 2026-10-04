<?php
// A deferred constant initializer runs the same conversion walk as the
// operator does at run time: non-numeric warnings, precision-loss
// deprecations, and refusals, all reported on the line it was written on.
// An inherited initializer is the declaring class's and runs once.

const A = +"0foo";
const B = -"3x";
const D = 7 / "2abc";
const F = 1 << 1.5;
const G = 9 >> "1z";
var_dump(A, B, D, F, G);

class X {
    const C = 1 % 1.5;
    const Q = 2.5 % 2;
    const int T = UNDEF_R . "x";
    const U = UNDEF_Q + 1;
}
class Y extends X {}

echo "-- child first\n";
var_dump(Y::C);
var_dump(X::C);
echo "-- parent first\n";
var_dump(X::Q);
var_dump(Y::Q);

define('UNDEF_R', "s");
foreach (['Y', 'X'] as $c) {
    try {
        var_dump(constant("$c::T"));
    } catch (TypeError $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

try {
    var_dump(Y::U);
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
define('UNDEF_Q', 5);
var_dump(Y::U, X::U);

class Z {
    const H = 1.5 % "";
    const N = -[1];
    const S = 3 / "0x";
}
foreach (['H', 'N', 'S'] as $k) {
    try {
        var_dump(constant("Z::$k"));
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
