<?php
// The value of an assignment, read back.
$a = 1;
$b = ($a = 5);
$a = 7;
var_dump($a, $b);
// Through a reference: `$e` gets the value, not the box, so a later write to
// `$c` moves `$d` and leaves `$e` alone.
$c = 2;
$e = ($d = &$c);
$c = 9;
var_dump($c, $d, $e);
// Chained the usual way round.
$p = $q = $r = 3;
var_dump($p, $q, $r);
// An assignment whose target already holds a reference writes through it.
$s = 1;
$t = &$s;
$u = ($t = 4);
var_dump($s, $t, $u);
echo "done\n";
