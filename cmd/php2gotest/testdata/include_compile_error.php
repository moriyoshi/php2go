<?php
// An included file that does not compile fails where the include runs: a
// ParseError the program can catch, located in the file, and a compile
// fatal printed through `@`, after which the shutdown functions still run.
function load() {
    try {
        include __DIR__ . '/include_compile_error_parse.inc';
    } catch (ParseError $e) {
        echo get_class($e), ": ", $e->getMessage(), " @ ", basename($e->getFile()), ":", $e->getLine(), "\n";
        echo $e->getTraceAsString(), "\n";
    }
}
load();
try {
    require __DIR__ . '/include_compile_error_parse.inc';
} catch (ParseError $e) {
    echo "again at line ", $e->getLine(), "\n";
}
register_shutdown_function(function () {
    echo "shutdown\n";
});
echo "before\n";
@include __DIR__ . '/include_compile_error_fatal.inc';
echo "not reached\n";
