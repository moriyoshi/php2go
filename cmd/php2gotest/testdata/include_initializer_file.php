<?php
// An included class's initializers, and an included file's own constant
// expressions, report the included file when they fail.
require __DIR__ . '/include_initializer_file.inc';
try {
    var_dump(Included::BAD);
} catch (Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), " @ ", basename($e->getFile()), ":", $e->getLine(), "\n";
}
try {
    var_dump(new WithDefault);
} catch (Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), " @ ", basename($e->getFile()), ":", $e->getLine(), "\n";
}
class Local {
    const BAD = 1 % 0;
}
try {
    var_dump(Local::BAD);
} catch (Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), " @ ", basename($e->getFile()), ":", $e->getLine(), "\n";
}
