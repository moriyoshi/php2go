<?php
// A tick function may not switch fibers: the engine blocks the switch around
// ZEND_TICKS, and starting, resuming, throwing into or suspending a fiber
// there is `FiberError: Cannot switch fibers in current execution context`
// (fibers/ticks). Suspending checks "outside a fiber" first. Outside the tick
// function the same fibers switch as usual.
//
// Also: `Fiber::suspend()` written with no argument outside a fiber, whose
// frame shows no argument.

// Held by a class rather than a global: a file body that makes a call that
// may suspend is compiled as a state machine, and its variables are then not
// the globals (a separate gap).
class Box {
    public static $parked;
    public static $attempts = 0;
}
Box::$parked = new Fiber(function () {
    $got = Fiber::suspend('parked');
    echo "parked got $got\n";
});
Box::$parked->start();

function tick() {
    if (Box::$attempts++ > 0) {
        return;
    }
    $parked = Box::$parked;
    $tries = [
        'suspend' => fn () => Fiber::suspend('from tick'),
        'start' => fn () => (new Fiber(fn () => print("never\n")))->start(),
        'resume' => fn () => $parked->resume('from tick'),
        'throw' => fn () => $parked->throw(new Exception('from tick')),
    ];
    foreach ($tries as $what => $try) {
        try {
            $try();
            echo "$what: allowed\n";
        } catch (FiberError $e) {
            echo "$what: ", get_class($e), ": ", $e->getMessage(), "\n";
        }
    }
}

$inner = new Fiber(function () {
    register_tick_function('tick');
    declare(ticks=1) {
        echo "ticking\n";
    }
    unregister_tick_function('tick');
    echo "suspending normally\n";
    return Fiber::suspend('ok');
});
var_dump($inner->start());
$inner->resume('done');
var_dump($inner->getReturn());
Box::$parked->resume('after');

try {
    Fiber::suspend();
} catch (FiberError $e) {
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
}
