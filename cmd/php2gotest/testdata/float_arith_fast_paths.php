<?php
// Two plain numbers -- ints or floats, not strings or bools -- take a fast
// path for + - * / < <= (OWNERSHIP-PERF.md). Each pair here is one where the
// fast path could disagree with the general one: an int beside a float
// compares as two doubles, NAN is unordered, an int past 2^53 loses precision
// as a double, overflow and infinity, and operands that are *not* plain
// numbers must still go the long way.

function ops($a, $b)
{
    var_dump($a < $b, $a <= $b, $b < $a, $b <= $a, $a <=> $b, $a == $b);
    var_dump($a + $b, $a - $b, $a * $b, $b != 0 ? $a / $b : "div0");
}

$pairs = [
    [1, 1.5], [2, 2.0], [NAN, 1], [1, NAN], [NAN, NAN], [0.1, 0.2],
    [PHP_INT_MAX, 1.0], [9007199254740993, 9007199254740992.0],
    [1e308, 10], [-0.0, 1], [INF, -INF], [7, 2.0], [3, 4],
    ["3", 1.5], [true, 2.5], [null, 0.5], ["1e2", 1.0],
];
foreach ($pairs as [$a, $b]) {
    echo "-- ", var_export($a, true), " ", var_export($b, true), "\n";
    ops($a, $b);
}
var_dump(max(NAN, 1), max(1, NAN));
