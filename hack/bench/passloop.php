<?php
// An array grown in a loop and handed to a function that only reads it, every
// time round. A by-value parameter shares the caller's array; if the share is
// never handed back, the caller's next append copies the whole array, and the
// loop is quadratic.
function total($a) { return count($a); }
function grow($n) {
    $arr = [];
    $sum = 0;
    for ($i = 0; $i < $n; $i++) {
        $arr[] = $i;
        $sum += total($arr);
    }
    return $sum;
}
$t = hrtime(true); $s = grow(20000); echo "passloop ", round((hrtime(true)-$t)/1e9, 3), " ($s)\n";
