<?php
// An engine class's method gets a stack frame like any other call, and the
// frame names the method as the **engine declares it** -- `Fiber->getReturn()`
// for any spelling of the call, not the folded name the dispatch looks up by.
// `start` and `resume` are already lowercase, which is why only the others
// showed the gap.
//
// The frame also has to still be on the stack when the error's trace is
// attached. It was popped first, so the trace opened at the call site with the
// method that raised missing from it.

function show(callable $f) {
    try { $f(); } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n", $e->getTraceAsString(), "\n--\n";
    }
}

show(function () { $f = new Fiber(function () { return 1; }); $f->getReturn(); });
show(function () { $f = new Fiber(function () { return 1; }); $f->GETRETURN(); });
show(function () { $f = new Fiber(function () { return 1; }); $f->resume(); });
show(function () { $f = new Fiber(function () { return 1; }); $f->throw(new Exception("x")); });
show(function () { $f = new Fiber(function () { return 1; }); $f->start(); $f->start(); });

// A backtrace taken *inside* a fiber sits on top of the resumer's frames.
$f = new Fiber(function () { echo (new Exception("in"))->getTraceAsString(), "\n--\n"; });
$f->start();

// The successful calls still answer, and still say nothing about a frame.
$g = new Fiber(function () { return 7; });
var_dump($g->isStarted(), $g->isSuspended(), $g->isRunning(), $g->isTerminated());
$g->start();
var_dump($g->isTerminated(), $g->getReturn());
