<?php
// A frame-reading function called by name is refused inside its own frame.
function f() {
    $g = "compact";
    try {
        $g("a");
    } catch (Error $e) {
        echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
    }
    $h = "func_get_arg";
    try {
        $h();
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
    }
}
f();
