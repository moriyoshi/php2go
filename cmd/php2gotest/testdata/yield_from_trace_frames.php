<?php
// A generator reached through `yield from` runs with the delegating
// generator's frame beneath it: its own frame names the `yield from` line,
// and the outer one names the driver -- the foreach's line, or an internal
// function for ->next().

function inner() {
    yield 1;
    debug_print_backtrace();
    yield 2;
    throw new Exception("from inner");
}

function outer() {
    yield from inner();
}

foreach (outer() as $v) {
    echo "got $v\n";
    if ($v == 2) break;
}

$g = outer();
$g->current();
$g->next();
try {
    $g->next();
} catch (Exception $e) {
    echo $e->getTraceAsString(), "\n";
}
