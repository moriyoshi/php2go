<?php
// Literal parameter defaults in the reflectors' __toString, as the
// engine's format_default_value writes them: NULL, true/false, floats at
// precision 14 with ".0" kept, escaped single-quoted strings, arrays keyed
// only when not a list, a float-only parameter's int default widened.
function f($a = 1, $b = 'x', $c = null, $d = [], $e = [1, 'a' => 2], $f = 1.5, $g = true, $m = -1, $n = [[1]], string $o = "", ?int $p = null, $q = "a'b", $r = __LINE__, $s = 1 + 2, $t = 0.1) {}
echo new ReflectionFunction('f');
function g(float $x = 1, $a2 = [1,2], $b2 = [5=>1], $c2 = ['a'], $d2 = [1, [2, 'k'=>3]], $e2 = 1.0, $f2 = -0.0, $g2 = 1e100, $h2 = "l\nm", $i2 = false, $k2 = 'ab\\c', $l2 = [true, null, 1.5], $m2 = 0x10, $n2 = 100000000000000000000, ...$v) {}
echo new ReflectionFunction('g');
function h($a = 1, $b) {}
echo new ReflectionFunction('h');
function k($s = "é\x01\t'", $t = [1 => 'a', 0 => 'b'], $z = 1e15, $y = 0.00001, $x = 123456789012345678) {}
echo new ReflectionFunction('k');
