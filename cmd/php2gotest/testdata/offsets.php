<?php
$s = "abc";
var_dump($s[1]);
var_dump($s[-1]);
var_dump($s["1"]);
var_dump($s[9]);
try { var_dump($s["x"]); } catch (TypeError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$n = null; var_dump($n[0]);
$i = 5; var_dump($i[0]);
$f = 1.5; var_dump($f[0]);
$t = true; var_dump($t[0]);
$fa = false; var_dump($fa[0]);
// `??` stays silent over all of it.
var_dump($s[9] ?? "d", $n[0] ?? "d", $i[0] ?? "d");
