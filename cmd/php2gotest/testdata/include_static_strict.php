<?php
declare(strict_types=1);
// Strictness is the calling file's: a strict file calling a function
// declared in a weak one refuses what the weak file would convert.
require __DIR__ . '/include_static_weak.inc';
try {
    var_dump(takes_int(1.0));
} catch (TypeError $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
// The weak file's own call converts.
var_dump(call_from_weak());
