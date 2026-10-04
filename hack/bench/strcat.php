<?php
// bench.php's strcat on its own: `.=` in a loop, which a string that is copied
// on every append makes quadratic.
function strcat($n) {
    $str = "";
    while ($n-- > 0) {
        $str .= "hello\n";
    }
    return strlen($str);
}
$t = hrtime(true); $len = strcat(200000); echo "strcat ", round((hrtime(true)-$t)/1e9, 3), " ($len)\n";
