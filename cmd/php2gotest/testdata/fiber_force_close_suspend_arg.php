<?php
// A `Fiber::suspend($v)` in a finally block a force-close runs is refused as
// "in a force-closed fiber", not "outside of a fiber": the destroyed fiber is
// the one the unwind runs in, though nothing entered it. The bare form and the
// argument form take the same check.
$f = new Fiber(function () {
    try { Fiber::suspend(1); } finally {
        try { Fiber::suspend(2); } catch (FiberError $e) { echo $e->getMessage(), "\n"; }
    }
});
$f->start();
unset($f);
echo "end\n";
$g = new Fiber(function () {
    try { Fiber::suspend(); } finally {
        try { Fiber::suspend(); } catch (FiberError $e) { echo "bare: ", $e->getMessage(), "\n"; }
    }
});
$g->start();
unset($g);
echo "done\n";
