<?php
// `Fiber::suspend()` with no fiber running is a **PHP error with a message**,
// not a missing capability: `FiberError: Cannot suspend outside of a fiber`,
// and the trace names the call that made it.
//
// Reaching a suspending body's ordinary entry point means nothing is resuming
// it, so the body is driven there and the diagnostic comes out. Refusing to
// compile it reported a gap where PHP has an answer -- and a script whose top
// level suspends is exactly that case.

try {
    $value = Fiber::suspend(1);
} catch (Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n", $e->getTraceAsString(), "\n--\n";
}

// The same through a call, which is the shape that needs the driving rather
// than a bare terminator.
function deeper($v) { return Fiber::suspend($v); }
try {
    deeper("x");
} catch (Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n", $e->getTraceAsString(), "\n--\n";
}

// Inside a fiber the same call is ordinary, so the diagnostic must not fire.
$f = new Fiber(function () { return Fiber::suspend("in") . "!"; });
var_dump($f->start());
var_dump($f->resume("R"));
var_dump($f->getReturn());
