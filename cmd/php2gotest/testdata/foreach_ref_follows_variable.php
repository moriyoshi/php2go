<?php
// A by-reference foreach over a variable holds the variable, made a
// reference, and re-reads it at every step: an array assigned to it in the
// body is walked from then on, from that array's internal pointer
// (gh19613). The same array under another name goes on where it was; an
// unset of the variable leaves the loop the array it had; a scalar ends the
// loop with a warning on the foreach's line.

$g = [1, 2, 3]; $n = 0;
foreach ($g as $k => &$x) {
    echo "$k=$x\n";
    if ($n++ == 0) { $g = [10, 20, 30, 40]; }
}
unset($x);
echo json_encode($g), "\n";

$g = [1, 2, 3]; $n = 0;
foreach ($g as $k => &$x) {
    echo "$k=$x\n";
    if ($n++ == 1) { $g = ['a' => 5, 'b' => 6]; }
}
unset($x);

// Written in place: the same array, so nothing restarts.
$g = [1, 2, 3];
foreach ($g as $k => &$x) {
    echo "$k=$x\n";
    $x *= 10;
    if ($k == 0) { $g[] = 4; }
}
unset($x);
echo json_encode($g), "\n";

// Handed to another name and back: still the same array.
$g = [1, 2, 3]; $n = 0;
foreach ($g as $k => &$x) {
    echo "$k=$x\n";
    if ($n++ == 1) { $h = $g; $g = $h; }
}
unset($x);

// Through a second name bound to the variable.
$d = [1, 2, 3]; $e = &$d; $n = 0;
foreach ($d as $k => &$x) {
    echo "$k=$x\n";
    if ($n++ == 0) { $e = [7, 8]; }
}
unset($x, $e);

// The new array's internal pointer is where the walk resumes; a copy whose
// pointer ran off the end starts at 0.
$g = [1, 2, 3]; $n = 0;
foreach ($g as $k => &$x) {
    echo "$k=$x\n";
    if ($n++ == 0) { $g2 = [5, 6, 7]; next($g2); $g = $g2; }
}
unset($x);
$g = [1, 2, 3]; $n = 0;
foreach ($g as $k => &$x) {
    echo "$k=$x\n";
    if ($n++ == 0) { $g2 = [4, 5]; end($g2); next($g2); $g = $g2; }
}
unset($x);

// An unset leaves the loop its array; a later assignment is to a fresh
// variable the loop does not hold.
$g = [1, 2, 3]; $n = 0;
foreach ($g as $k => &$x) {
    echo "$k=$x\n";
    if ($n++ == 0) { unset($g); $g = [9, 9, 9, 9]; }
}
unset($x);

// A scalar ends it, warning on the foreach's line.
$g = [1, 2, 3]; $n = 0;
foreach ($g as $k => &$x) {
    echo "$k=$x\n";
    if ($n++ == 0) { $g = null; }
}
unset($x);
var_dump($g);

// A property is re-read the same way.
$o = new stdClass; $o->a = [1, 2, 3]; $n = 0;
foreach ($o->a as $k => &$x) {
    echo "$k=$x\n";
    if ($n++ == 0) { $o->a = [9, 8, 7, 6]; }
}
unset($x);

// Inside a function, over a parameter.
function walk(array $p) {
    $n = 0;
    foreach ($p as $k => &$v) {
        echo "$k=$v\n";
        if ($n++ == 0) { $p = ['x' => 1, 'y' => 2]; }
    }
    return $p;
}
echo json_encode(walk([1, 2])), "\n";

// gh19613: the inner loop's variable is given a new array on every step.
$a = [1];
$i = 0;
foreach ($a as &$v) {
    $a[0] = $a;
    foreach ($v as &$w) {
        $w = $a;
        if ($i++ == 8) {
            echo "done $i\n";
            break 2;
        }
    }
}
