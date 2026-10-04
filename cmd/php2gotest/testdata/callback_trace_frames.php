<?php
// A callback gets a stack frame like any other call, and the frame's location
// is `[internal function]` when a library function is what reached it -- the
// engine calls it from inside itself and says so. Compiled output pushed no
// frame at all for a callable named by string, so a callback that threw opened
// its trace at the builtin that invoked it, one line short.
//
// The name on the frame is the **declared** spelling, not the callable's:
// `array_map("BOOM", ...)` reaching `function Boom()` reads `Boom(1)`.
//
// The exception is a callback the engine invokes *at* a place in the script --
// an error handler runs where the diagnostic was raised, so its frame names
// that file and line rather than `[internal function]`.

function Boom($x) { throw new Exception("b"); }
function Cmp($p, $q) { throw new Exception("c"); }
function Handler($n, $m) {
    echo "handler: ", $m, "\n", (new Exception("h"))->getTraceAsString(), "\n--\n";
    return true;
}
class K {
    static function s($x) { throw new Exception("s"); }
    function m($x) { throw new Exception("m"); }
}

function show(callable $f) {
    try { $f(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n--\n"; }
}

show(fn() => array_map('BOOM', [1]));
show(fn() => array_map(['K', 's'], [1]));
show(fn() => array_map([new K, 'm'], [1]));
show(fn() => array_map('K::s', [1]));
show(fn() => array_map(function ($x) { throw new Exception("z"); }, [1]));
show(function () { $a = [3, 1]; usort($a, 'CMP'); });
show(function () { $a = [3, 1]; array_walk($a, 'Boom'); });
show(fn() => call_user_func_array('Boom', [5]));
show(fn() => array_reduce([1], 'Boom'));

// Folded by the compiler into a direct call, so no callback frame at all and
// the callee names the real call site.
show(fn() => call_user_func('Boom', 7));
show(function () { $g = 'Boom'; $g(3); });

set_error_handler('Handler');
$z = $undefinedVariable;
restore_error_handler();

// A library function's own frame is pushed **before** its arguments are
// checked, so the error the check raises opens inside it: an output handler
// invoked with the buffer and a phase is an ArgumentCountError against
// `strtoupper` located at `[internal function]`, not at the `ob_end_flush()`
// that reached it.
ob_start("strtoupper");
echo "ab";
ob_end_flush();

try { str_repeat("a", []); } catch (Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n", $e->getTraceAsString(), "\n--\n";
}
try { strlen(); } catch (Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n", $e->getTraceAsString(), "\n--\n";
}
show(fn() => array_map('strtoupper', [[1]]));
