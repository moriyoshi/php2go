<?php
// Since PHP 8.4 `exit` is a real function: the argument goes through ordinary
// coercion and a bad one is a **catchable** TypeError that leaves the program
// running. `die` is the same function, and the diagnostics say `exit()` either
// way.

function attempt(callable $f) {
    try { $f(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
attempt(function () { die([1]); });
attempt(function () { exit(new stdClass); });
echo "still running\n";

// The declared type is `string|int`, and which arm a value takes decides
// whether it is printed or is the status. Measured: an object with
// `__toString` takes the string arm; null, bool and float take the int one.
class S { function __toString() { return "[S]"; } }
$shown = [];
foreach ([1, "s", true, false, null, 1.5, new S] as $v) {
    $shown[] = gettype($v);
}
var_dump($shown);

// A `finally` does not run on the way out, and neither does anything after.
function leaves() {
    try {
        echo "in try\n";
        exit("bye\n");
    } finally {
        echo "finally must not run\n";
    }
}
echo "before\n";
leaves();
echo "after must not run\n";
