<?php
// A fiber force-closed at the end of the request may not suspend again: the
// suspend in its finally is a FiberError, thrown there and uncaught.
$fiber = new Fiber(function (): void {
    try {
        Fiber::suspend();
    } finally {
        echo "finally\n";
        Fiber::suspend();
    }
});
$fiber->start();
echo "done\n";
