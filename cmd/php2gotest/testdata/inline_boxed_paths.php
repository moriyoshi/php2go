<?php
// Untyped arithmetic and comparisons on two ints, a by-value foreach over an
// array, and `$list[] = v` on a local take inline paths at the site
// (gogen boxedIntFast, php.SnapIter, php.PackedAppend). Each case is one the
// inline path has to hand back to the general code, or has to get right.

function ops($a, $b)
{
    return [$a + $b, $a - $b, $a * $b, $a < $b, $a <= $b, $a == $b, $a != $b, $a === $b, $a !== $b];
}
var_dump(ops(PHP_INT_MAX, 1));
var_dump(ops(PHP_INT_MIN, 1));
var_dump(ops(46341, 46341));
var_dump(ops(1 << 32, 1 << 32));
var_dump(ops(-7, 3));
var_dump(ops("10", 9));
var_dump(ops(1, "1"));
var_dump(ops(1.5, 2));
var_dump(ops(null, 0));

// A by-value foreach iterates the array as it was: writes inside the loop are
// not seen, and an element bound by reference is read when it is reached.
function walk(array $a)
{
    $seen = [];
    foreach ($a as $k => $v) {
        $a[] = "added";
        unset($a[$k + 1]);
        $seen[] = "$k=$v";
    }
    return [$seen, count($a)];
}
var_dump(walk([1, 2, 3]));

$x = 10;
$withRef = [1, 2, 3];
$withRef[1] = &$x;
foreach ($withRef as $v) {
    $x = 99;
    echo $v, " ";
}
echo "\n";

// By reference, over a generator, over an object: not the snapshot walk.
$byRef = [1, 2, 3];
foreach ($byRef as &$r) {
    $r *= 2;
    if (count($byRef) < 5) {
        $byRef[] = 7;
    }
}
unset($r);
var_dump($byRef);

function gen()
{
    yield "a" => 1;
    yield "b" => 2;
}
foreach (gen() as $k => $v) {
    echo "$k$v ";
}
$o = new stdClass();
$o->p = 1;
$o->q = 2;
foreach ($o as $k => $v) {
    echo "$k$v ";
}
foreach (new ArrayIterator([3, 4]) as $v) {
    echo $v, " ";
}
echo "\n";

// Nested loops over one array, with break and continue.
$grid = [1, 2, 3];
foreach ($grid as $i) {
    foreach ($grid as $j) {
        if ($j == 2) {
            continue;
        }
        if ($i == 3) {
            break;
        }
        echo "$i$j ";
    }
}
echo "\n";

// Appends: copy-on-write, a hole at the end, string keys, a reference-bound
// list, growth past the capacity, and an array appended to itself.
$a = [1, 2];
$b = $a;
$a[] = 3;
var_dump($a, $b);

$h = [1, 2, 3];
unset($h[2]);
$h[] = 4;
var_dump($h);

$s = ["k" => 1];
$s[] = 2;
var_dump($s);

$target = [];
$alias = &$target;
$alias[] = "via alias";
$target[] = "direct";
var_dump($target);

$big = [];
for ($i = 0; $i < 20; $i++) {
    $big[] = $i;
}
echo count($big), " ", $big[19], "\n";

$self = [1];
$self[] = $self;
var_dump($self);
