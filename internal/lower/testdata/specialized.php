<?php
// Fixture for the *specialized* golden: the IR as the emitter actually reads
// it, after lower.Specialize has rewritten operations into intrinsic calls.
// The plain goldens are pre-specialization on purpose -- Specialize is a choice
// about the consumer, and an interpreter over the same IR would make a
// different one -- so without this file nothing pins how a call.intrinsic
// renders.

function narrow(int $a, int $b) {
    $sum = $a + $b;
    $prod = $sum * 2;
    return $prod - 1;
}

function general($x, $y) {
    // Nothing is known about either operand, so these stay on the general
    // variant and still render as calls.
    return $x + $y;
}

function mixedKinds(string $s, int $n) {
    // concat has one variant; the comparisons have one each. They are calls
    // regardless -- having a single variant is not a reason to stay an opcode.
    $t = $s . "x";
    $eq = $t === $s;
    $shift = $n << 2;
    $cmp = $n <=> 3;
    return [$t, $eq, $shift, $cmp];
}

// Knowing a value **is** an array, rather than that it may be one, selects the
// variants with the narrower effect contract: no rewind to run, no offset on a
// non-container to throw, no Countable to ask.
function containers(array $rows, int $i) {
    $n = count($rows);
    $first = $rows[$i];
    foreach ($rows as $v) {
        $n = $n + 1;
    }
    return [$n, $first];
}
