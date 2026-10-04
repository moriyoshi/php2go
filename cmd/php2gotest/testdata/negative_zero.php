<?php
// PHP prints negative zero, and Zend constant-folds the arithmetic that makes
// one -- so `0.0 * -1` reaches the emitter as a literal. Rendering it as the Go
// literal `-0` compiles and is silently **positive** zero: Go has no
// negative-zero constant, `-0` is an untyped integer and `-0.0` folds the same
// way. Only `math.Copysign(0, -1)` carries the sign.
//
// The interpreter was right throughout, because it never goes through a Go
// literal. Every case below is a way the value can arrive or be printed.
var_dump(-0.0);
var_dump(0.0 * -1);
var_dump(-1 * 0.0);
var_dump(0.0 / -1);
var_dump((float)"-0");
var_dump(-0.0 === 0.0);
var_dump(1 / -INF);
echo -0.0, "\n";
echo 0.0 * -1, "\n";
print_r(-0.0); echo "\n";
var_export(-0.0); echo "\n";
var_dump((string)(-0.0));
var_dump(round(-0.4));
var_dump(floor(-0.0));
var_dump(json_encode(-0.0));
var_dump(abs(-0.0));
var_dump([-0.0]);
var_dump(-0.0 <=> 0.0);
var_dump(serialize(-0.0));
$x = -0.0;
var_dump($x, $x + 0, $x * 1);
var_dump(INF, -INF, NAN);
