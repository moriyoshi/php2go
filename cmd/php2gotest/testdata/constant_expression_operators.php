<?php
// A default parameter that names a constant is kept by the engine as an AST and
// evaluated **on first use**, so the whole operator set has to be renderable.

const TEN = 10;

function ops(
    $add = 1 + 1,
    $shiftLeft = 1 << 2,
    $shiftRight = TEN >> 1,
    $concat = "foo" . "bar",
    $mul = TEN * 10,
    $div = TEN / 4,
    $mod = TEN % 3,
    $pow = 2 ** 3,
    $bitAnd = TEN & 6,
    $bitOr = TEN | 5,
    $bitXor = TEN ^ 6,
    $bitNot = ~TEN,
    $negate = -TEN,
    $identity = +TEN,
    $not = !TEN,
) {
    var_dump($add, $shiftLeft, $shiftRight, $concat, $mul, $div, $mod, $pow);
    var_dump($bitAnd, $bitOr, $bitXor, $bitNot, $negate, $identity, $not);
}
ops();

// The parser has no greater-than opcode -- it emits the smaller-than form with
// the operands swapped -- so `>` and `>=` are their own AST kinds and the
// source order has to be swapped back.
function comparisons(
    $greater = TEN > 5,
    $greaterEqual = TEN >= 10,
    $smaller = TEN < 5,
    $smallerEqual = TEN <= 10,
    $equal = TEN == "10",
    $notEqual = TEN != 3,
    $identical = TEN === 10,
    $notIdentical = TEN !== "10",
    $spaceship = TEN <=> 3,
    $xor = (TEN > 5) xor (TEN > 50),
) {
    var_dump($greater, $greaterEqual, $smaller, $smallerEqual, $equal);
    var_dump($notEqual, $identical, $notIdentical, $spaceship, $xor);
}
comparisons();

// `&&` and `||` short-circuit, so the right side is only reached when the left
// does not already decide.
function shortCircuit($and = TEN > 5 && TEN < 20, $or = TEN > 50 || TEN === 10) {
    var_dump($and, $or);
}
shortCircuit();

// The operators that can **refuse** throw when the call happens, not when the
// program starts.
function byZero($x = 1 / 0) { var_dump($x); }
try { byZero(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
function moduloZero($x = 1 % 0) { var_dump($x); }
try { moduloZero(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
function negativeShift($x = 1 << -1) { var_dump($x); }
try { negativeShift(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
function negativeShiftRight($x = 1 >> -1) { var_dump($x); }
try { negativeShiftRight(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// Falling off the end of a function that declares a return type is a TypeError
// naming the **qualified** function. `__toString` is where this turns up
// without anyone writing a type, because its `string` return is implicit.
class Silent { function __toString() {} }
try { echo new Silent; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
function typed(): string {}
try { typed(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
function nullableTyped(): ?string {}
try { var_dump(nullableTyped()); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
class Scoped { function method(): int {} }
try { (new Scoped)->method(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
// `void` declares exactly this, so it is not an error.
function nothing(): void {}
var_dump(nothing());

// A constant expression runs the **same operator** as ordinary code, so it owes
// the same diagnostic walk: `AcceptsArithmetic` decides what each operand earns
// and stops at the first failure.
const BAD_SUB = [1, 2] - [0];
try { var_dump(BAD_SUB); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

const BAD_MUL = "abc" * 2;
try { var_dump(BAD_MUL); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

const BAD_AND = [1] & 1;
try { var_dump(BAD_AND); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// Addition of two arrays is the union, not an arithmetic error -- which is why
// the walk has to ask the operator and not just the operand types.
const UNION = [1, 2] + [0, 0, 3];
var_dump(UNION);

// Concatenation accepts anything a string can be made from, so it is not
// guarded the way the arithmetic operators are.
const JOINED = 1 . "x" . 2.5;
var_dump(JOINED);
