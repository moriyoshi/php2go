<?php
$n = 10;
$byValue = function ($x) use ($n) { return $x + $n; };
echo $byValue(5), "\n";
// Changing $n after capture must NOT be visible: `use ($n)` copied it.
$n = 100;
echo $byValue(5), "\n";

$c = 0;
$byRef = function () use (&$c) { $c++; };
$byRef(); $byRef();
echo $c, "\n";
// ...and a change outside IS visible inside, because the box is shared.
$c = 40;
$byRef();
echo $c, "\n";

$double = fn($x) => $x * 2;
echo $double(4), "\n";

// A closure returned from a function keeps its captures.
function makeAdder($k) { return function ($x) use ($k) { return $x + $k; }; }
$add3 = makeAdder(3);
echo $add3(4), "\n";

// Nested closures.
$outer = function ($a) { return function ($b) use ($a) { return $a . "-" . $b; }; };
$inner = $outer("x");
echo $inner("y"), "\n";
