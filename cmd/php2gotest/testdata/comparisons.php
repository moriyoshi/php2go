<?php
// The five comparisons that used to bypass the intrinsic layer, through a
// second operator table in the emitter: `<`, `<=`, `!=`, `!==` and `xor`.
//
// **The operands are parameters, not literals.** `arith_contracts.php` tested
// only constants for two weeks, so Zend folded every one of them at compile
// time and the file passed while `$a & $b` did not compile at all. A comparison
// between literals proves nothing about the operator.
function probe($a, $b) {
    var_dump($a < $b, $a <= $b, $a > $b, $a >= $b);
    var_dump($a != $b, $a !== $b, $a == $b, $a === $b);
    var_dump($a xor $b);
    return ($a < $b) ? "lt" : "ge";
}

echo probe(1, 2), "\n";
echo probe(2, 2), "\n";
// Two numeric strings compare numerically, so these are equal.
echo probe("10", "1e1"), "\n";
echo probe("abc", "abd"), "\n";
// Arrays compare by size first, then element by element.
echo probe([1], [1, 2]), "\n";
echo probe(null, false), "\n";
echo probe(1.5, 1), "\n";
