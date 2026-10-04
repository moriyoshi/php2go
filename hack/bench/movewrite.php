<?php
// A fresh array stored into a variable and then written. The store needs no
// copy -- nothing else holds the array -- and if it shares anyway, the write
// after it copies the whole array for a holder that no longer exists.
function build($n) { $a = []; for ($i = 0; $i < $n; $i++) { $a[] = $i; } return $a; }
function run($n) {
    $sum = 0;
    for ($i = 0; $i < $n; $i++) {
        $b = build(200);
        $b[] = $i;
        $sum += count($b);
    }
    return $sum;
}
$t = hrtime(true); $s = run(50000); echo "movewrite ", round((hrtime(true)-$t)/1e9, 3), " ($s)\n";
