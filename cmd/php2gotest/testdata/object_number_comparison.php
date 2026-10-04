<?php
// **Against a number, an object stands in as 1** -- and the engine announces
// that conversion with a Notice. Measured in both directions and for every
// relation: `1 == $o` true, `2 > $o` true, `0 < $o` true, `$o > 1` false.
//
// The notice names the type being converted *to*: int for an int operand,
// float for a float. Against a string, a bool, null, an array or another
// object no conversion is attempted, so there is no notice and the
// "object outranks everything" rule applies instead.
class T {}
class S { function __toString() { return "s"; } }
$o = new T;

foreach ([0, 1, 2, -5, 0.5, 1.0, 2.5] as $n) {
    printf("n=%s: n>o=%d n<o=%d n==o=%d o>n=%d o<n=%d n!=o=%d\n",
        var_export($n, true), $n > $o, $n < $o, $n == $o, $o > $n, $o < $n, $n != $o);
}

// No conversion, so no notice, for every other type.
var_dump("str" > $o, true > $o, null > $o, [1] > $o, (new T) > $o);

// A __toString does not excuse it: the conversion attempted is to a number.
var_dump(1 > new S);

// **An enum case is never converted.** It is uncomparable against a number --
// every relation false, in both directions -- and the engine says nothing,
// including for a backed enum whose value is literally 1.
enum E { case A; }
enum F: int { case X = 1; }
var_dump(E::A > 1, 1 > E::A, E::A == 1, F::X == 1, E::A > 2.5);

// Identity never converts, so it never notices.
var_dump($o === 1, $o !== 1);
echo "done\n";
