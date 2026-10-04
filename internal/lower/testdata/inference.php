<?php
// Fixtures for the type inference, exercised through the real pipeline.

// A loop-carried accumulator. Promoted, `$s` is a block parameter joined from
// `0` and the add's own result -- so on the first round the join reads a value
// that is still being computed. Treating that as "unknown" rather than "not yet
// known" made it Any, and Any is absorbing under a union, so the add could
// never narrow.
function accumulate(int $x, int $y): int {
    $s = 0;
    for ($i = 0; $i < $y; $i++) {
        $s = $s + $x;
    }
    return $s;
}

// The same variable holding two types at different points. A place carries one
// fact for the whole function -- the join over every store -- where an SSA
// value carries one per definition, so only the promoted form can narrow the
// second use.
function reassigned(int $n) {
    $x = "s";
    echo $x;
    $x = $n;
    return $x + 1;
}

// A container's element type is what makes everything downstream of it typed.
// `[1, 2, 3]` is an `array<int, int>`, so the iterator yields int and the sum
// narrows -- none of which a flat type mask can express.
function overTypedLiteral(): int {
    $s = 0;
    foreach ([1, 2, 3] as $v) {
        $s = $s + $v;
    }
    return $s;
}

// Built rather than folded: array.new then array.append, so the element type is
// the union of what was appended.
function overBuilt(int $a, int $b): int {
    $arr = [$a, $b];
    $s = 0;
    foreach ($arr as $v) {
        $s = $s + $v;
    }
    return $s;
}

// Mixed elements must NOT narrow: the walk has to diagnose the string.
function overMixed(int $a, string $b) {
    $arr = [$a, $b];
    $s = 0;
    foreach ($arr as $v) {
        $s = $s + $v;
    }
    return $s;
}

// The shape alone -- nothing known about the elements -- is what the container
// variants ask for.
function shaped(array $rows, int $i) {
    return count($rows) + count($rows[$i]);
}

// `?array` may be null, so the same three operations must keep the general
// implementations: on null, `count()` throws and `$m[$i]` throws.
function maybeShaped(?array $rows, int $i) {
    return count($rows) + count($rows[$i]);
}

// The soundness half of the array-backed iterator. A Traversable's current(),
// key() and next() are user code: they can print, throw and re-enter. Narrowing
// here would skip all of it, which is a wrong answer rather than a slow one.
function overTraversable(Traversable $t) {
    $n = 0;
    foreach ($t as $v) {
        $n = $n + 1;
    }
    return $n;
}

// `?array` is not an array: the null path reaches the general constructor.
function overMaybeArray(?array $a) {
    $n = 0;
    foreach ($a as $v) {
        $n = $n + 1;
    }
    return $n;
}
