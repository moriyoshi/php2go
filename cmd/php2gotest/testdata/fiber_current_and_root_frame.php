<?php
// A fiber is the current fiber while its body runs, even one that never
// suspends: getCurrent() answers it and resuming it from inside is the
// FiberError for a fiber that is not suspended. Its callable appears once
// in a trace, as a frame the engine made.

$plain = new Fiber(function () {
    var_dump(Fiber::getCurrent() === $GLOBALS['plain']);
    try {
        Fiber::getCurrent()->resume();
    } catch (FiberError $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
});
$plain->start();
var_dump(Fiber::getCurrent());

$traced = new Fiber(function (int $x) {
    try {
        throw new Exception("inside");
    } catch (Exception $e) {
        echo $e->getTraceAsString(), "\n";
    }
    Fiber::suspend($x);
});
$traced->start(1);

// An argument the engine passed has no call site to name.
$typed = new Fiber(function (int $x) { Fiber::suspend($x); });
try { $typed->start('text'); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
