<?php
function counter(array $rows) {
    $seen = 0;
    foreach ($rows as $r) {
        $reply = yield $r;
        $seen = $seen + 1;
        if ($reply) {
            yield $seen;
        }
    }
    return $seen;
}

function plain(array $rows): int {
    $n = 0;
    foreach ($rows as $r) {
        $n = $n + 1;
    }
    return $n;
}

// One slot written with two different representations. The frame must declare
// the join, or the emitter would store an int into an object field.
function retyped($n) {
    $x = new \stdClass;
    yield 1;
    $x = 5;
    return $x;
}

// A yield in one arm of an if. The continuation is its own block and the join
// lands after it, which is why a continuation that uses its resumed value has
// only the resumption as a predecessor.
function joined($c) {
    if ($c) {
        $r = yield 1;
    } else {
        $r = 2;
    }
    return $r;
}
