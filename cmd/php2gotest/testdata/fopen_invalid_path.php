<?php
// fopen refuses an empty path and one with a NUL byte before any wrapper is
// asked.
foreach (["", "a\0b", "php://memory\0x"] as $path) {
    try {
        var_dump(fopen($path, "r"));
    } catch (ValueError $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
