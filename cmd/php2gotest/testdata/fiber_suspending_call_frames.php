<?php
// A function reached through a suspending call gets a stack frame, and the
// frame names the line it was **called from** -- captured when the call
// happened, not when the frame runs, because by the time a resume re-enters it
// the caller's own position has moved on.
//
// The fiber body itself gets one too, located at `[internal function]`: the
// engine enters it from inside `start()` or `resume()` rather than from a line
// of PHP. A generator's driver pushes the equivalent frame itself, which is why
// only a fiber's body needs one here.

function level2(): void {
    Fiber::suspend();
    throw new Exception("from level2");
}

function level1(): void {
    level2();
}

$f = new Fiber(function (): void { level1(); });
$f->start();
try {
    $f->resume();
} catch (Throwable $e) {
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n--\n";
}

// A backtrace taken *without* throwing sees the same stack.
function reporter(): void {
    Fiber::suspend();
    echo (new Exception("here"))->getTraceAsString(), "\n--\n";
}
$g = new Fiber(function (): void { reporter(); });
$g->start();
$g->resume();

// The arguments are on the frames.
function arged(int $n, string $s): void {
    Fiber::suspend();
    throw new Exception("arged");
}
$h = new Fiber(function (): void { arged(7, "xy"); });
$h->start();
try { $h->resume(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n--\n"; }
