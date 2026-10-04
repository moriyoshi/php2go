<?php
// A by-value foreach walks a copy-on-write share of the array rather than a
// copy of its keys and values (OWNERSHIP-PERF.md). Each shape here is one
// where the two differ, or where the share must not leak: an element holding
// a reference is read when the loop reaches it, writes to the array inside
// the loop do not reach the walk, a nested walk over the same array, the
// array's internal pointer, and an array that turns into a hash mid-build.

$a = [1, 2, 3];
$r = &$a[1];
foreach ($a as $v) {
    $r = 9;
    echo $v, " ";
}
echo "\n";
unset($r);

$b = [1, 2, 3];
foreach ($b as $k => $v) {
    $b[] = $v;
    unset($b[2]);
    echo "$k=$v ";
}
echo count($b), " ", implode(",", $b), "\n";

$c = [1, 2, 3];
foreach ($c as $x) {
    foreach ($c as $y) {
        $c[$y] = $x * $y;
    }
}
var_dump($c);

$d = [10, 20, 30];
next($d);
foreach ($d as $v) {
    echo $v, " ";
}
echo current($d), "\n";

// A list that stops being packed while being built, then is walked.
$e = [];
for ($i = 0; $i < 20; $i++) {
    $e[] = $i;
}
unset($e[3], $e[7]);
$f = $e;
$f["k"] = "v";
$f[3] = "back";
foreach ($f as $k => $v) {
    echo "$k:$v ";
}
echo "\n", isset($e[3]) ? "e3" : "no e3", " ", count($e), " ", count($f), "\n";
