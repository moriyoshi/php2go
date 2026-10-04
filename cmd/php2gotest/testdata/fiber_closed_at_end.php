<?php
// A fiber still suspended when the request ends is force-closed, as a
// generator is: its finally blocks run, before the shutdown functions.
register_shutdown_function(function () {
    echo "shutdown\n";
});
$f = new Fiber(function () {
    try {
        Fiber::suspend(1);
        echo "not reached\n";
    } finally {
        echo "fiber finally\n";
    }
});
var_dump($f->start());
function g() {
    try {
        yield 1;
    } finally {
        echo "generator finally\n";
    }
}
$g = g();
$g->current();
$done = new Fiber(function () {
    echo "ran to the end\n";
});
$done->start();
echo "end\n";
