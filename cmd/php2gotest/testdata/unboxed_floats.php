<?php
// Arithmetic proved to be float -- or an int from a small known set of
// constants -- is carried as a Go float64 (lower.FloatReps). Each function
// here is built to catch one way that could go wrong.

// The shape the pass exists for: a loop-carried value that starts as the int
// literal 0 and becomes a float. Every value in the loop rides unboxed.
function orbit($cr, $ci)
{
    $cr = $cr * 1.0;
    $ci = $ci * 1.0;
    $z = 0;
    $Z = 0;
    for ($k = 0; $t = $z * $z - $Z * $Z + $cr, $Z = 2 * $z * $Z + $ci, $z = $t, $k < 50; $k++) {
        if ($z * $z + $Z * $Z > 4) {
            break;
        }
    }
    return $k;
}
foreach ([[-0.5, 0.5], [0.3, 0.6], [-1.2, 0.1], [0.25, 0.0]] as [$r, $i]) {
    echo orbit($r, $i), " ";
}
echo "\n";

// Signed zero: int 0 * int -3 is int 0, but 0.0 * -3.0 is -0.0. After one
// round PHP holds +0 and echoes "0"; carrying the int as a float would give
// "-0".
function signedZero($n)
{
    $z = 0;
    for ($i = 0; $i < $n; $i++) {
        $z = $z * -3 + -0.0;
    }
    return $z;
}
var_dump(signedZero(1), signedZero(2));

// A value that may still be the int 0 when it is observed must print as an
// int, so it cannot be boxed from a float64.
function maybeInt($n)
{
    $z = 0;
    if ($n) {
        $z = 1.5;
    }
    var_dump($z * 2);
}
maybeInt(0);
maybeInt(1);

// Fused multiply-add: 0.1 * 10.0 rounds to exactly 1.0, so adding -1.0 is 0.
// A fused a*b+c keeps the product's low bits and answers 5.55E-17.
function fma(float $a, float $b, float $c)
{
    return $a * $b + $c;
}
var_dump(fma(0.1, 10.0, -1.0));

// Division: an int divisor of zero and a float one are the same error, and an
// exact int quotient stays an int while it is in the loop.
function divide(float $x, $d)
{
    try {
        $y = $x * 2.0;
        return $y / ($d * 1.0);
    } catch (DivisionByZeroError $e) {
        return $e->getMessage();
    }
}
var_dump(divide(1.5, 0), divide(1.5, -0.0), divide(1.5, 4));
function halves($n)
{
    $q = 8;
    for ($i = 0; $i < $n; $i++) {
        $q = $q / 2 + 0.25;
    }
    return $q;
}
var_dump(halves(0), halves(1), halves(3));

// NaN is unordered: every comparison answers false, whichever side it is on.
function unordered(float $x)
{
    $y = $x * 1e308;
    $z = $y * 10.0;
    $n = $z - $z;
    var_dump($n < 1.0, $n <= 1.0, 1.0 < $n, 1.0 <= $n, $n > 1.0, $n >= 1.0);
}
unordered(1.0);

// An int of unknown value converts only where PHP would convert it: beside a
// float. PHP_INT_MAX loses precision as a double in both.
function mixed(int $i)
{
    $f = $i * 0.5;
    return $f + $i;
}
var_dump(mixed(3), mixed(PHP_INT_MAX));

// Comparing a small constant carried as a float against an int of unknown
// value, near the constant.
function near(int $k)
{
    $z = 0;
    $hits = 0;
    for ($j = 0; $j < 3; $j++) {
        if ($z < $k) {
            $hits++;
        }
        if ($z <= $k) {
            $hits += 10;
        }
        $z = $z + 0.5;
    }
    return $hits;
}
var_dump(near(0), near(1), near(-1));

// Counting in ints stays exact past 2^53, where a double would round.
function counter(int $start)
{
    $x = $start;
    for ($i = 0; $i < 3; $i++) {
        $x = $x + 1;
    }
    return $x;
}
var_dump(counter(9007199254740992));

// A guarded operation -- an untyped operand -- is certainly a float only when
// the other side is one. `$x * 2` on an int is an int, and so is `+ 0` after.
function twice($x)
{
    return $x * 2 + 0;
}
var_dump(twice(3), twice("4"), twice(1.5));
