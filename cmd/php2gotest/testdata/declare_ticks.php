<?php
// declare(ticks=N): every Nth statement calls the registered tick
// functions in order, with their bound arguments; unregistering stops
// one, and what a tick throws is thrown at the statement.
function t1($x = "none") { echo "t1 $x\n"; }
function t2() { echo "t2\n"; $a = 1; $b = 2; }
var_dump(register_tick_function("t1", "arg"));
register_tick_function("t2");
declare(ticks=2) {
    $a = 1;
    $b = 2;
    $c = 3;
    $d = 4;
}
unregister_tick_function("t2");
declare(ticks=1) {
    $e = 5;
}
try {
    register_tick_function("nope");
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
unregister_tick_function("t1");
register_tick_function(function () { throw new Exception("from tick"); });
try {
    declare(ticks=1) {
        $f = 6;
        echo "after\n";
    }
} catch (Exception $e) {
    echo "caught ", $e->getMessage(), "\n";
}
