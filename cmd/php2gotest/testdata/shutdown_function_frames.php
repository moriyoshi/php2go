<?php
// A shutdown function is called by the engine with no script running: its
// frame renders as [internal function], and an uncaught throw from it is
// fatal and ends the shutdown sequence.
function f($x) { g(); }
function g() { throw new Exception("in g"); }
register_shutdown_function(function () {
    echo "first\n";
    debug_print_backtrace();
});
register_shutdown_function("f", 5);
register_shutdown_function(function () { echo "never\n"; });
echo "end\n";
