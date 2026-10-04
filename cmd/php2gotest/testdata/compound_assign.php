<?php
// Compound assignment across every operator and every kind of target.
//
// This lowered to a single `assign_op $x, $v, op=concat` instruction carrying
// the operator as an **attribute**, which the emitter resolved through its own
// operator table -- a second copy of the catalogue, which had drifted the same
// way the opcode table had. It spelled the bitwise operators `bit_and` where
// the engine emits `bw_and`, and had no entry for `div`, `mod`, `sl` or `sr`,
// so seven of these did not compile at all.
//
// It is now a read, an intrinsic call and a write, which also gives compound
// assignment the intrinsic's effect contract for the first time.

function locals($a, $b) {
    $x = $a; $x += $b;  var_dump($x);
    $x = $a; $x -= $b;  var_dump($x);
    $x = $a; $x *= $b;  var_dump($x);
    $x = $a; $x /= 2;   var_dump($x);
    $x = $a; $x %= 5;   var_dump($x);
    $x = $a; $x **= 2;  var_dump($x);
    $x = $a; $x &= $b;  var_dump($x);
    $x = $a; $x |= $b;  var_dump($x);
    $x = $a; $x ^= $b;  var_dump($x);
    $x = $a; $x <<= 2;  var_dump($x);
    $x = $a; $x >>= 1;  var_dump($x);
    $s = "q"; $s .= "z"; var_dump($s);
}
locals(12, 10);
locals(-7, 3);

// The value of a compound assignment is the assigned value.
function asExpression($n) {
    $a = $n;
    $b = ($a .= "!");
    var_dump($a, $b);
    $c = 1;
    var_dump($c += 4, $c);
}
asExpression("x");

// Array elements, object properties and static properties are *places*: the
// container must be evaluated once, not once per read and once per write.
class Box {
    public $p = 1;
    public static $s = "s";
    public $arr = [];
}
function places($k) {
    $arr = ["a" => 1, "b" => "x"];
    $arr["a"] += 10;
    $arr["b"] .= "y";
    $arr[$k] = 5;
    $arr[$k] *= 3;
    var_dump($arr);

    $o = new Box();
    $o->p += 41;
    // `$o->arr["z"] <<= 3` is a dim inside a prop, which the emitter refuses
    // as `dim.ref on a non-local` -- a pre-existing gap in nested places,
    // unrelated to compound assignment. Recorded in TODO.md.
    var_dump($o->p);

    Box::$s .= "t";
    var_dump(Box::$s);
}
places("c");

// A compound assignment now carries the intrinsic's effect contract, which the
// emitter's own table had no way to express.
function contracts($n, $z, $arr) {
    try { $q = $n; $q /= $z; } catch (DivisionByZeroError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
    try { $q = $n; $q %= $z; } catch (DivisionByZeroError $e) { echo $e->getMessage(), "\n"; }
    try { $q = $arr; $q -= 1; } catch (TypeError $e) { echo "T: ", $e->getMessage(), "\n"; }
    $u = $arr; $u += [9, 9, 9];
    var_dump($u);
}
contracts(1, 0, [1, 2]);

// Reading an undefined variable warns even though the read has no separate
// instruction in the source.
function warnsOnUndefined() {
    $f .= "x";
    var_dump($f);
    $g += 1;
    var_dump($g);
}
warnsOnUndefined();
