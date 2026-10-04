<?php
// A Fiber is constructed once: calling `__construct` again -- before it
// starts or after it finished -- is `FiberError: Cannot call constructor
// twice`, raised in the constructor's own frame.
$fiber = new Fiber(fn() => 123);
foreach ([false, true] as $run) {
    if ($run) {
        $fiber->start();
        var_dump($fiber->getReturn());
    }
    try {
        $fiber->__construct(fn() => 321);
    } catch (FiberError $e) {
        echo get_class($e), ": ", $e->getMessage(), " | ", $e->getTrace()[0]['function'], "\n";
    }
}
