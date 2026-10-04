<?php
// A `#[\Deprecated]` function's deprecation is raised before the call is
// entered: a handler that throws leaves a trace with the handler and the
// caller's frames, and none for the deprecated function itself.

set_error_handler(function (int $no, string $msg) { throw new ErrorException($msg, 0, $no); });

#[\Deprecated]
function old() { echo "not reached\n"; }

class K {
    #[\Deprecated("use nu()")]
    public function m() { echo "not reached\n"; }
}

function caller() { old(); }

foreach ([fn () => caller(), fn () => (new K)->m()] as $f) {
    try { $f(); } catch (ErrorException $e) {
        echo $e->getMessage(), "\n";
        foreach ($e->getTrace() as $frame) {
            echo "  ", $frame['class'] ?? '', $frame['type'] ?? '', $frame['function'], "\n";
        }
    }
}
