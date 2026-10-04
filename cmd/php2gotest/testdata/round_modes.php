<?php
// round()'s third argument settles a value exactly halfway: away from zero
// (the default), towards it, to the even neighbour, or to the odd one -- at
// any precision, negative values included; anything else is a ValueError.

foreach ([PHP_ROUND_HALF_UP, PHP_ROUND_HALF_DOWN, PHP_ROUND_HALF_EVEN, PHP_ROUND_HALF_ODD] as $m) {
    echo $m, ": ";
    foreach ([[2.5, 0], [-2.5, 0], [3.5, 0], [1.55, 1], [-1.45, 1], [5.045, 2], [7, 0], [1.2345, 3], [-0.5, 0]] as [$v, $p]) {
        echo var_export(round($v, $p, $m), true), " ";
    }
    echo "\n";
}
var_dump(round(2.5, mode: PHP_ROUND_HALF_EVEN));
try {
    round(1.5, 0, 99);
} catch (ValueError $e) {
    echo $e->getMessage(), "\n";
}
