<?php
// FiberError is final and its constructor refuses: only the engine makes
// one, whichever way the program asks.

try {
    new FiberError;
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
try {
    $c = "FiberError";
    new $c("message", 1);
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

$fiber = new Fiber(function () {});
$fiber->start();
try {
    $fiber->resume();
} catch (FiberError $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
