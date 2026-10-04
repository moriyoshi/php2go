<?php
// `throw` refuses a non-object with `Can only throw objects` and an object
// that is not a Throwable with its own message; a subclass of an engine
// exception is thrown as itself.

class Mine extends RuntimeException {}
foreach ([1, "str", null, new stdClass, new ArrayObject([])] as $what) {
    try {
        throw $what;
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
try {
    throw new Mine("mine");
} catch (RuntimeException $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
