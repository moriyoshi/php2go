<?php
// `$list[$i] op= v` and `$list[$i]++` on a packed list read the element and
// write it back in place (php.PackedAt, php.PackedSet) when nothing else
// shares the array. Each case is one the in-place write must decline or get
// right.

function bump(array $a, $i)
{
    $a[$i] += 10;
    $a[$i]++;
    return $a;
}

// Copy-on-write: the caller's array, and a copy taken first, are untouched.
$orig = [1, 2, 3];
$copy = $orig;
$out = bump($orig, 1);
$copy[0] .= "x";
var_dump($orig, $copy, $out);

// An element bound by reference: the write goes through the box.
$list = [5, 6, 7];
$r = &$list[2];
$list[2] *= 3;
$list[2]--;
var_dump($r, $list);

// A hole left by unset, and a key past the end: both warn and create.
$holes = [1, 2, 3];
unset($holes[1]);
$holes[1] += 4;
$holes[5] ??= 9;
$holes[6] .= "tail";
var_dump($holes);

// Nested: the inner list is reached through the outer one.
$grid = [[1, 2], [3, 4]];
for ($i = 0; $i < 2; $i++) {
    for ($j = 0; $j < 2; $j++) {
        $grid[$i][$j] *= 10;
    }
}
$alias = $grid;
$grid[1][1] += 1;
var_dump($grid, $alias[1][1]);

// A literal array written in a loop: every iteration starts from the literal.
for ($k = 0; $k < 3; $k++) {
    $lit = [0, 0, 0];
    $lit[$k] += $k + 1;
    echo implode(",", $lit), "\n";
}

// A string offset refuses the compound assignment before reading anything.
$s = "abc";
try {
    $s[0] .= "z";
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
var_dump($s);
