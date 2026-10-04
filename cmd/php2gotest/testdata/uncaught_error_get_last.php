<?php
// An uncaught exception is the last error a shutdown function sees:
// error_get_last() answers E_ERROR with `Uncaught ` and the chain closed by
// `thrown` -- the location is in the file and line fields rather than the
// text (bug61767).

register_shutdown_function(function () {
    echo "Shutting down\n";
    $e = error_get_last();
    var_dump($e["type"], $e["line"], basename($e["file"]));
    echo $e["message"], "\n";
});

function thrower() {
    throw new LogicException("inner");
}
try {
    thrower();
} catch (LogicException $l) {
    throw new RuntimeException("outer", 3, $l);
}
