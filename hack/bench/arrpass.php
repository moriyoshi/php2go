<?php
function sum($a) { $s = 0; foreach ($a as $v) { $s += $v; } return $s; }
function build($n) { $a = []; for ($i = 0; $i < $n; $i++) { $a[] = $i; } return $a; }
$a = build(100);
$t = hrtime(true); $s = 0; for ($i = 0; $i < 200000; $i++) { $s += sum($a); } echo "pass ", round((hrtime(true)-$t)/1e9, 3), "\n";
$t = hrtime(true); for ($i = 0; $i < 200000; $i++) { $b = build(100); } echo "return ", round((hrtime(true)-$t)/1e9, 3), "\n";
