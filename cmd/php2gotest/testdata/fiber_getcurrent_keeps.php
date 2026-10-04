<?php
// A fiber body that stores Fiber::getCurrent() keeps its own fiber alive:
// unsetting the local destroys nothing, and the fiber is closed only when
// the request ends.
$keep = null;
$f = new Fiber(function () use (&$keep) {
    $keep = Fiber::getCurrent();
    try {
        Fiber::suspend();
    } finally {
        echo "finally at end\n";
    }
});
$f->start();
unset($f);
echo "after unset\n";
echo "end\n";
