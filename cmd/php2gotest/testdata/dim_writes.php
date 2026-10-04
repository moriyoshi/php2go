<?php
$s = "abc"; $s[1] = "X"; var_dump($s);
$t = "abc"; $t[5] = "Z"; var_dump($t);
$u = "abc"; $u[1] = "LONG"; var_dump($u);
$a = null; $a[0] = 1; var_dump($a);
$b = "x"; try { $b[] = 1; } catch (Error $e) { echo "E: ", $e->getMessage(), "\n"; }
$c = 5; try { $c[0] = 1; } catch (Error $e) { echo "E: ", $e->getMessage(), "\n"; }
$d = []; $r = &$d; $r[0] = "via-ref"; var_dump($d);
$un[2] = "auto"; var_dump($un);
