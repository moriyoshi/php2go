<?php
// A copy of a growing array taken into a local that is only read, every time
// round. The copy shares the elements; if its share is never handed back when
// it dies, the next append copies the whole array, and the loop is quadratic.
function grow($n) {
    $arr = [];
    $sum = 0;
    for ($i = 0; $i < $n; $i++) {
        $arr[] = $i;
        $copy = $arr;
        $sum += count($copy);
    }
    return $sum;
}
$t = hrtime(true); $s = grow(20000); echo "copyloop ", round((hrtime(true)-$t)/1e9, 3), " ($s)\n";
