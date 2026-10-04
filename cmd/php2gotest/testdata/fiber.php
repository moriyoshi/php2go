<?php
// Fibers in compiled output. A generator can be modelled by saving a program
// counter because it only suspends at a `yield` written in its own body; a
// fiber's `Fiber::suspend()` parks the whole stack it started. The lowering
// rewrites a suspending body into a state machine (`internal/lower/cps.go`) and
// `php.Fiber` drives it, so what is checked here is the round trip: the frame
// is built from the callable **and start()'s arguments**, parks at the
// suspension, and resumes where it left off.
$f = new Fiber(function (int $a) {
    $got = Fiber::suspend($a + 1);
    return "end:$got";
});
var_dump($f->isStarted(), $f->isSuspended(), $f->isTerminated());
var_dump($f->start(1));
var_dump($f->isStarted(), $f->isSuspended(), $f->isTerminated());
var_dump($f->resume('back'));
var_dump($f->getReturn(), $f->isTerminated(), $f->isSuspended());

// `Fiber::suspend()` with no argument parks with NULL, which the lowering
// carries as an empty argument list rather than an explicit null.
$bare = new Fiber(function () { Fiber::suspend(); return 'after'; });
var_dump($bare->start());
var_dump($bare->resume('ignored'), $bare->getReturn());

// A callable with no suspension in it is not compiled as a state machine at
// all. The engine runs it to completion on the first start(), and start()
// answers NULL because nothing parked.
$plain = new Fiber(function () { return 7; });
var_dump($plain->start(), $plain->getReturn(), $plain->isTerminated());

// throw() surfaces at the suspension point, where the body may catch it.
$caught = new Fiber(function () {
    try {
        Fiber::suspend('a');
    } catch (Exception $e) {
        return 'caught:' . $e->getMessage();
    }
    return 'not reached';
});
var_dump($caught->start());
var_dump($caught->throw(new Exception('boom')), $caught->getReturn());

// Every misuse of the API is a FiberError the program can catch, and
// getReturn() has three distinct refusals rather than one.
$refuse = new Fiber(function () { Fiber::suspend(1); return 'r'; });
try {
    $refuse->resume(1);
} catch (Throwable $e) {
    var_dump(get_class($e), $e->getMessage());
}
try {
    $refuse->getReturn();
} catch (Throwable $e) {
    var_dump(get_class($e), $e->getMessage());
}
$refuse->start();
try {
    $refuse->start();
} catch (Throwable $e) {
    var_dump(get_class($e), $e->getMessage());
}
try {
    $refuse->getReturn();
} catch (Throwable $e) {
    var_dump(get_class($e), $e->getMessage());
}

// getCurrent() answers the **object**, so `$f === Fiber::getCurrent()` inside
// a fiber's own body is true. Fibers nest: resuming one from inside another
// makes the inner one current until it hands control back.
var_dump(Fiber::getCurrent());
$outer = new Fiber(function () {
    $inner = new Fiber(function () {
        Fiber::suspend('in');
        return 'i';
    });
    var_dump($inner->start());
    Fiber::suspend('out');
    var_dump($inner->resume('r'), $inner->getReturn());
    return 'done';
});
var_dump($outer->start());
var_dump($outer->resume(null), $outer->getReturn());
var_dump(Fiber::getCurrent());
