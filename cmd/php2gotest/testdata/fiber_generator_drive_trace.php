<?php
// The trace through a driver call a fiber body makes on a generator that may
// suspend. The call is split into a `GeneratorDrive`, which pushed no frame of
// its own -- `Generator->current()` was missing under the body -- and after a
// resume the parked chain continued with only the innermost generator's
// frame, located at the line that resumed the fiber rather than at
// `[internal function]` and the `yield from`.
function first($s) {
    if ($s) Fiber::suspend();
    throw new LogicException('bad first');
    yield 1;
}
// Thrown on the first run: no suspension at all.
$f = new Fiber(function () {
    $g = first(false);
    try {
        $g->current();
    } catch (LogicException $e) {
        echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
    }
});
$f->start();
// Thrown after a resume, through a `yield from` and directly.
function inner() {
    Fiber::suspend();
    throw new LogicException('bad inner');
    yield 1;
}
function third() {
    yield from inner();
}
$f3 = new Fiber(function () {
    $g3 = third();
    try {
        $g3->current();
    } catch (LogicException $e) {
        echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
    }
});
$f3->start();
$f3->resume();
function plain() {
    Fiber::suspend();
    throw new LogicException('bad plain');
    yield 1;
}
$f4 = new Fiber(function () {
    $g3 = plain();
    try {
        $g3->current();
    } catch (LogicException $e) {
        echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
    }
});
$f4->start();
$f4->resume();
