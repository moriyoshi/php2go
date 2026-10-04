<?php
// Values proved to be ints are carried as a Go int64 (lower.IntReps), and a
// `++` or `--` is native only where a branch proves it cannot overflow. Each
// function here is built to catch one way that proof could be wrong.

// The bound holds right up to the edge: the last step lands on PHP_INT_MAX,
// still an int, and the loop ends.
function toTheEdge()
{
    for ($i = PHP_INT_MAX - 2; $i < PHP_INT_MAX; $i++) {
        var_dump($i);
    }
    var_dump($i);
}
toTheEdge();

// `<=` against a literal below the maximum bounds the step too.
function inclusive()
{
    for ($i = PHP_INT_MAX - 3; $i <= PHP_INT_MAX - 1; $i++) {
    }
    var_dump($i);
}
inclusive();

// A counter with no test of its own overflows into a float.
function unbounded()
{
    $x = PHP_INT_MAX - 1;
    $y = PHP_INT_MIN + 1;
    for ($k = 0; $k < 3; $k++) {
        $x++;
        $y--;
        var_dump($x, $y);
    }
}
unbounded();

// A bound that is a float does not bound an int: 1e19 is past PHP_INT_MAX.
function floatBound(float $limit)
{
    $n = 0;
    for ($i = PHP_INT_MAX - 1; $i < $limit; $i++) {
        var_dump($i);
        if (++$n > 2) {
            break;
        }
    }
}
floatBound(1e19);

// The comparison has to be on the value being stepped, not on a sibling.
function sibling()
{
    for ($i = PHP_INT_MAX - 1, $j = 0; $j < 3; $i++, $j++) {
        var_dump($i);
    }
}
sibling();

// The true edge has to dominate the step: after the `if` joins, the step runs
// whether or not the comparison held.
function afterJoin(int $i)
{
    if ($i < 5) {
        echo "small\n";
    }
    $i++;
    var_dump($i);
}
afterJoin(PHP_INT_MAX);
afterJoin(1);

// Counting down, bounded from below.
function down()
{
    for ($i = PHP_INT_MIN + 2; PHP_INT_MIN < $i; $i--) {
        var_dump($i);
    }
    var_dump($i);
    for ($i = 3; $i > 0; $i--) {
        echo $i;
    }
    echo "\n";
}
down();

// Modulo keeps ints, truncating toward zero, and PHP_INT_MIN % -1 is 0.
function mods(int $a, int $b)
{
    for ($i = -7; $i < 8; $i++) {
        echo $i % 3, " ", $i % -4, " ";
    }
    echo "\n";
    try {
        var_dump($a % $b);
    } catch (DivisionByZeroError $e) {
        echo $e->getMessage(), "\n";
    }
}
mods(PHP_INT_MIN, -1);
mods(5, 0);

// Bitwise operators and the equality family on native ints.
function bits()
{
    for ($i = 0; $i < 8; $i++) {
        echo $i & 3, $i | 8, $i ^ 5, " ";
        var_dump($i == 3, $i === 3, $i != 2, $i !== 4, $i <= 5);
    }
}
bits();

// A native int reaching a float operation converts, and reaching anything else
// boxes as the int it is.
function mixed()
{
    $s = 0.0;
    $a = [];
    for ($i = 0; $i < 5; $i++) {
        $s = $s + $i * 0.5;
        $a[$i] = $i;
    }
    var_dump($s, $a, $i < 5.5, $i == 5.0);
}
mixed();

// A bound on the wrong side proves nothing: `$i < 0` does not stop `$i--`
// from running past PHP_INT_MIN.
function wrongSide()
{
    $n = 0;
    for ($i = PHP_INT_MIN + 1; $i < 0; $i--) {
        var_dump($i);
        if (++$n > 2) {
            break;
        }
    }
}
wrongSide();

// The true edge has to be the only way in: a `goto` into the guarded block
// reaches the step without the comparison having held.
function viaGoto(int $i, int $n, bool $skip)
{
    if ($skip) {
        goto body;
    }
    if ($i < $n) {
        body:
        $i++;
    }
    var_dump($i);
}
viaGoto(PHP_INT_MAX, 0, true);
viaGoto(1, 5, false);

// `$i + c` is an int only when the bound leaves room for c: under
// `$i < PHP_INT_MAX` there is room for 1 and not for 2.
function stepByLiteral()
{
    for ($i = PHP_INT_MAX - 2; $i < PHP_INT_MAX; $i++) {
        var_dump($i + 1, 1 + $i, $i + 2);
    }
    for ($i = PHP_INT_MAX - 4; $i <= PHP_INT_MAX - 2; $i++) {
        var_dump($i + 2, $i + 3);
    }
    for ($i = PHP_INT_MIN + 2; PHP_INT_MIN < $i; $i--) {
        var_dump($i - 1, $i + -1, $i - 2);
    }
    for ($i = 0; $i < 3; $i++) {
        var_dump($i + 5, $i - 1);
    }
}
stepByLiteral();

// A bound held in a variable leaves room for exactly 1: `$i < $n` says
// nothing about `$i + 2` when `$n` is PHP_INT_MAX.
function variableBound(int $n)
{
    for ($i = PHP_INT_MAX - 2; $i < $n; $i++) {
        var_dump($i + 1, $i + 2);
    }
}
variableBound(PHP_INT_MAX);

// The false edge of `a < b` is `b <= a`, which is not strict: with a variable
// bound it leaves no room, since both can be PHP_INT_MIN. The false edge of
// `a <= b` is strict, and leaves room for 1.
function falseEdges(int $x, int $y)
{
    if ($x < $y) {
        var_dump("smaller");
    } else {
        var_dump($x - 1);
    }
    if ($y <= $x) {
        var_dump("not below");
    } else {
        var_dump($x + 1);
    }
    if ($x < PHP_INT_MIN + 1) {
        var_dump("minimum");
    } else {
        var_dump($x - 1);
    }
}
falseEdges(PHP_INT_MIN, PHP_INT_MIN);
falseEdges(PHP_INT_MAX - 1, PHP_INT_MAX);
falseEdges(5, 3);

// The false edge of `$x < 5` says `$x >= 5`: a floor, which bounds nothing
// upwards.
function floorOnly(int $x)
{
    if ($x < 5) {
        return 0;
    }
    return $x + 1;
}
var_dump(floorOnly(PHP_INT_MAX), floorOnly(7));
