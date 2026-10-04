<?php
function fibo_r($n) { return (($n < 2) ? 1 : fibo_r($n - 2) + fibo_r($n - 1)); }
$t = hrtime(true); $r = fibo_r(30); echo "fibo ", $r, " ", round((hrtime(true)-$t)/1e9, 3), "\n";
