<?php
// `match` compares with **identity**, and the engine's jump table is an
// optimisation over exactly that. The table is not an ordinary PHP array: it
// keeps `1` and `"1"` as distinct keys where an array literal would coerce the
// second into the first.

function pick($x) {
    return match ($x) {
        1, 2 => "small int",
        "1" => "the string one",
        "s" => "the string s",
        true => "the boolean",
        null => "the null",
        default => "default",
    };
}
foreach ([1, 2, "1", "s", true, null, 3, "other", 1.0, false] as $v) {
    echo gettype($v), " => ", pick($v), "\n";
}

// No default and no arm: an UnhandledMatchError naming the subject by value for
// a scalar and by type for anything else.
function strict($x) { return match ($x) { "only" => "hit" }; }
foreach ([5, "s", null, true, false, 1.5, 0.0, [1], new stdClass] as $v) {
    try {
        strict($v);
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

// A match arm is an expression, so it can be anything -- including one with
// side effects, which must run only for the arm that is taken.
function sideEffects($x) {
    $log = [];
    $mark = function ($tag) use (&$log) { $log[] = $tag; return $tag; };
    $r = match ($x) {
        1 => $mark("one"),
        2 => $mark("two"),
        default => $mark("none"),
    };
    return [$r, $log];
}
var_dump(sideEffects(1), sideEffects(2), sideEffects(9));

// match with no subject arms is the true-matching form.
function classify(int $n) {
    return match (true) {
        $n < 0 => "negative",
        $n === 0 => "zero",
        default => "positive",
    };
}
var_dump(classify(-5), classify(0), classify(7));
