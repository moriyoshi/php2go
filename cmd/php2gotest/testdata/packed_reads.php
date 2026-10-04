<?php
// Reading `$list[$i]` from a packed array takes the element straight from the
// bucket slice (php.ReadDim's packedRead). Each read here is one that path has
// to decline or get right: a hole left by unset, an element bound by
// reference, an index past either end, and a list that stopped being packed.

function readAll(array $a, array $keys)
{
    foreach ($keys as $k) {
        var_dump($a[$k] ?? "none");
        var_dump(@$a[$k]);
    }
}

$list = [10, 20, 30, 40];
unset($list[1]);
readAll($list, [0, 1, 2, 3, 4, -1]);

$x = 5;
$refs = [1, 2, 3];
$refs[1] = &$x;
$x = 99;
readAll($refs, [0, 1, 2]);
var_dump($refs[1]);

$hashed = [1, 2, 3];
$hashed["k"] = 4;
readAll($hashed, [0, 2, 3, "k"]);

function loud(array $a)
{
    for ($i = -1; $i < 5; $i++) {
        var_dump($a[$i]);
    }
}
$holes = [1, 2, 3, 4];
unset($holes[2]);
loud($holes);

// The element read is a value: writing to the copy does not reach the list.
$src = [[1], [2]];
$got = $src[1];
$got[] = 3;
var_dump($src);
