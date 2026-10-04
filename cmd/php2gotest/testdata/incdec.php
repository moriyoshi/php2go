<?php
$a = null; $a++; echo "null++ =[", $a, "] int1=", ($a === 1) ? "y" : "n", "\n";
$b = null; $b--; echo "null-- =[", $b, "]\n";
$c = true; $c++; echo "true++ =[", $c, "]\n";
$d = false; $d--; echo "false--=[", $d, "]\n";
$e = "5"; $e++; echo "\"5\"++ =[", $e, "] int6=", ($e === 6) ? "y" : "n", "\n";
$f = "z"; $f++; echo "\"z\"++ =[", $f, "]\n";
$g = "Az"; $g++; echo "\"Az\"++=[", $g, "]\n";
$h = "a9"; $h++; echo "\"a9\"++=[", $h, "]\n";
$i = "zz"; $i++; echo "\"zz\"++=[", $i, "]\n";
$j = ""; $j++; echo "\"\"++  =[", $j, "] isstr1=", ($j === "1") ? "y" : "n", "\n";
$k = ""; $k--; echo "\"\"--  =[", $k, "] int-1=", ($k === -1) ? "y" : "n", "\n";
$l = "a-b"; $l++; echo "\"a-b\"++=[", $l, "]\n";
$undef++; echo "undef++=[", $undef, "]\n";
