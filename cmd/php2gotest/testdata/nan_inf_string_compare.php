<?php
// A number against a numeric-looking string: NAN equals nothing, a numeric
// string that overflows is still numeric (and so equal to INF), and "INF" is
// not a number at all.

foreach ([[NAN, "NAN"], [INF, "1e1000"], [-INF, "-1e1000"], [INF, "INF"], [1.0, "1e0"], [0, "1e-1000"]] as [$a, $b]) {
    var_dump($a == $b, $a <=> $b, $a < $b);
}
var_dump("1e1000" == INF, "1e1000" > 1e308);
