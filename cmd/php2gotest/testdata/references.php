<?php
$a = 1;
$b = &$a;
$b = 2;
echo $a, $b, "\n";
$a = 5;
echo $a, $b, "\n";
$b++;
echo $a, $b, "\n";
$a += 10;
echo $a, $b, "\n";

function bump(&$x) { $x++; }
bump($a);
echo $a, $b, "\n";

function setIt(&$x, $v) { $x = $v; }
setIt($a, 99);
echo $a, $b, "\n";

// A by-value parameter must NOT alias, even when the argument is a reference.
function byValue($x) { $x = 1000; }
byValue($a);
echo $a, $b, "\n";

// A fresh local passed by reference is boxed at the call site.
$fresh = 3;
bump($fresh);
echo $fresh, "\n";
