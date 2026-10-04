<?php
// The diagnostic ORDER is observable: the left operand's deprecation precedes
// the TypeError, and the walk stops at the first failure so the right operand
// never reports.
try { var_dump(2.5 % ""); } catch (TypeError $e) { echo "T: ", $e->getMessage(), "\n"; }
try { var_dump("" % "2abc"); } catch (TypeError $e) { echo "T: ", $e->getMessage(), "\n"; }
// `+` is the only operator that takes arrays, and it is a union: the left
// operand's keys win.
var_dump([1,2] + [9,9,9]);
try { var_dump([1] - 1); } catch (TypeError $e) { echo "T: ", $e->getMessage(), "\n"; }
// Bitwise over two strings is bytewise and yields a string.
var_dump("ab" & "cd");
try { var_dump(1/0); } catch (DivisionByZeroError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { var_dump(1%0); } catch (DivisionByZeroError $e) { echo $e->getMessage(), "\n"; }
// PHP 8 loose equality: a non-numeric string is not coerced to 0.
var_dump(0 == "a", "1" == "01", "abc" == 0, "10" == "1e1");
var_dump(1 <=> 2, 2 <=> 1, 1 <=> 1);

// ---------------------------------------------------------------------------
// The same operators with RUN-TIME operands.
//
// Everything above is written with literals, and Zend folds a literal operation
// at compile time -- so those lines exercise the engine's constant folder and
// php2go's literal rendering, and the operator opcodes never reach the emitter
// at all. `"ab" & "cd"` above passed for two weeks while `$a & $b` did not
// compile: `intrinsicFor` spelled the opcode `bit_and` where the lowering emits
// `bw_and`, so the whole bitwise and shift family was refused as unsupported.
//
// TESTING.md's rule is "vary the value". This is the same rule one level down:
// vary whether the value is *known at compile time*, because that decides which
// code path is under test.
function ops($a, $b) {
    var_dump($a + $b, $a - $b, $a * $b, $a ** $b);
    var_dump($a & $b, $a | $b, $a ^ $b, ~$a);
    var_dump($a << 2, $a >> 1);
    var_dump($a == $b, $a === $b, $a != $b, $a !== $b);
    var_dump($a < $b, $a <= $b, $a > $b, $a >= $b, $a <=> $b);
}
ops(12, 10);
ops(-7, 3);

function fractional($a, $b) {
    var_dump($a / $b, $a % $b, $a . $b);
}
fractional(7, 2);

function mixedOperands($s, $n, $f) {
    // A run-time non-numeric string still warns and still stops the walk.
    try { var_dump($f % $s); } catch (TypeError $e) { echo "T: ", $e->getMessage(), "\n"; }
    var_dump($n . $s, $s == $n, $s === $n);
}
mixedOperands("2abc", 2, 2.5);

function byZero($n, $z) {
    try { var_dump($n / $z); } catch (DivisionByZeroError $e) { echo $e->getMessage(), "\n"; }
    try { var_dump($n % $z); } catch (DivisionByZeroError $e) { echo $e->getMessage(), "\n"; }
}
byZero(1, 0);

function negativeShift($n, $s) {
    try { var_dump($n << $s); } catch (ArithmeticError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
negativeShift(1, -1);

function stringBitwise($x, $y) {
    var_dump($x & $y, $x | $y, $x ^ $y);
}
stringBitwise("ab", "cd");

// An operand is evaluated ONCE.
//
// The guarded form of an intrinsic call names every operand, and
// ThrowUnsupportedOperands names two of them again, so the emitter expanded the
// rendered operand text at each site. An operand that reads a place is
// `php.ReadVar`, which *warns* on an undefined variable — so this printed the
// undefined-variable warning two and three times. Wrong output, not a slow path,
// and invisible to every case whose operands were all defined.
function evaluatedOnce() {
    var_dump($u + 1);
    var_dump($u2 - 1);
    var_dump($u3 * 2);
    var_dump($u4 . "x");
    $c = $u5;
    $c += 1;
    var_dump($c);
}
evaluatedOnce();

// Two ints do not make an int.
//
// `add.int` claimed its result was an int for as long as nothing asked the
// engine. It overflows to float, and so do `-` and `*`; `**` was already
// recorded as numeric. Bitwise really does stay int, which is why the two
// families are described differently — settled here rather than assumed.
function overflow($m, $one, $two) {
    var_dump($m + $one, $m * $two, $m - PHP_INT_MIN, PHP_INT_MIN - $one);
    var_dump($m << 1, $m & $m, $m | $one, $m ^ $one, ~$m, $m >> 1);
    var_dump(2 ** 62, 2 ** 63, (-2) ** 63);
    // A chain: the second operation sees int|float, not int.
    var_dump(($m + $one) * $two, ($m + $one) + $one);
}
overflow(PHP_INT_MAX, 1, 2);
overflow(3, 1, 2);

// Two numerics cannot diagnose, so a float operand narrows too — but a string,
// a bool and a null all reach the arithmetic walk, and the walk is what
// reports them.
function numerics($f, $i, $nan, $inf) {
    var_dump($f + $i, $f * $i, $f - $i, $f ** $i, $nan + $i, $inf - $inf);
    // Bitwise on a float deprecates, which is why it has no numeric variant.
    var_dump($f & $i, $f | $i);
}
numerics(1.5, 2, NAN, INF);

// A constant's type comes from the value the engine registered for it.
function constants($n) {
    var_dump(PHP_INT_SIZE * 8, E_ALL & ~E_NOTICE, PHP_INT_MAX % 7);
    var_dump(M_PI * 2, PHP_EOL . "x", PHP_INT_MAX + $n);
}
constants(1);
