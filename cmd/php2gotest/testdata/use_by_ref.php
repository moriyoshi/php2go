<?php
// `use (&$x)` shares the enclosing box: the closure's writes are visible
// outside, and later writes outside are visible inside.
//
// The position matters, and that is the point of this fixture. The engine
// encodes by-reference in **bit 0** of the bind opcode's extended_value and
// sets bit 5 on every capture after the first, so a test of `== 1` is right for
// a lone `use (&$x)` and wrong for every later one -- which made a
// by-reference capture in second position silently a copy.

$a = 1;
$b = 2;
$c = 3;

$first = function () use (&$a, $b) { $a++; return $a + $b; };
$second = function () use ($b, &$c) { $c++; return $b + $c; };
$both = function () use (&$a, &$c) { $a += 10; $c += 10; };
$none = function () use ($a, $b) { return $a + $b; };

var_dump($first(), $a);
var_dump($second(), $c);
$both();
var_dump($a, $c);

// A later write outside is visible inside, which a copy would not show.
$a = 100;
var_dump($first(), $a);

// The captured-by-value one still sees the value at capture time.
var_dump($none());

// Three captures, with the by-reference one last.
$x = 1;
$y = 2;
$z = 3;
$last = function () use ($x, $y, &$z) { $z = $x + $y; };
$last();
var_dump($z);
