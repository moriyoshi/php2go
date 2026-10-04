<?php
function fact($n) {
    $r = 1;
    $i = 1;
    while ($i <= $n) {
        $r = $r * $i;
        $i = $i + 1;
    }
    return $r;
}
$i = 1;
while ($i <= 6) {
    echo $i, "! = ", fact($i), "\n";
    $i = $i + 1;
}
