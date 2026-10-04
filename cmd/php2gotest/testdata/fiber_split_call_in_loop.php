<?php
// A call that may suspend splits its caller into a state machine, and a value
// the caller keeps in a Go local does not survive the re-entry. Values defined
// in the split block were moved into the frame; a loop's own values were not:
// mem2reg makes a `for` counter or a `foreach` iterator a block parameter
// read from other blocks, and after the first split call the counter printed
// empty and the `foreach` stopped after one element -- silently.
//
// None of the functions below suspends directly (a body that does is not
// promoted at all); each only makes calls that may.

function g() {
    yield 'a' => 1;
    $got = Fiber::suspend('in g');
    yield 'b' => $got;
    yield 'c' => 3;
}

function inner($what) {
    return Fiber::suspend($what);
}

function counted($gen) {
    // The counter, live across every split call in the body.
    for ($i = 0; $i < 5; $i++) {
        if (!$gen->valid()) break;
        echo $i, ": ", $gen->key(), " => ", $gen->current(), "\n";
        $gen->next();
    }
    echo "counted $i\n";
    return $i;
}

function summed() {
    // A foreach's iterator, and a value from before the loop.
    $prefix = "item";
    $sum = 0;
    foreach ([10, 20, 30] as $k => $v) {
        $got = inner("at $k");
        $sum += $v;
        echo "$prefix $k: $v ($got)\n";
    }
    echo "sum $sum\n";
    return $sum;
}

function caught() {
    // A value read in a catch handler after a suspension inside the try.
    $before = "kept";
    try {
        inner('in try');
        throw new Exception("thrown");
    } catch (Exception $e) {
        echo $before, " ", $e->getMessage(), "\n";
    }
    return $before;
}

// Each is a fiber's own body, so the call into it is the fiber's.
foreach (['counted', 'summed', 'caught'] as $body) {
    $f = new Fiber($body);
    $v = $f->start(...($body === 'counted' ? [g()] : []));
    while (!$f->isTerminated()) {
        echo "fiber gave $v\n";
        $v = $f->resume('R');
    }
    var_dump($f->getReturn());
}
