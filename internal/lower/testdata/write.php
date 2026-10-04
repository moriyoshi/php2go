<?php
function fill(array $rows): array {
    $rows['a']['b'] = 1;
    $rows[] = 2;
    return $rows;
}

class Reg {
    public static $n = 0;
}

// A static property is a plain slot owned by a class, so the write is one call
// like any other -- it was the last one lowering to a place plus a `store`.
function stash(int $v) {
    Reg::$n = $v;
}

// The read is a call too, and its silent form is a different intrinsic: an
// undeclared static property is an Error for the loud one and merely "not set"
// for the silent one.
function peek(): int {
    return Reg::$n + (Reg::$n ?? 0);
}
