<?php
// While a user error handler runs there is no handler: a diagnostic raised
// inside it takes the default path -- unless the handler installs one of its
// own, which then catches it. When the handler returns, the one that was
// running comes back, unless the handler left a replacement in place.

set_error_handler(function ($no, $msg) { echo "First: $msg\n"; });
set_error_handler(function ($no, $msg) {
    echo "Second: $msg\n";
    set_error_handler(function ($no, $msg) { echo "Internal: $msg\n"; });
    $inside++;
    restore_error_handler();
});
$a++;
$b++;

set_error_handler(function ($no, $msg) {
    echo "Plain: $msg\n";
    $nested++;
});
$c++;

set_error_handler(function ($no, $msg) {
    echo "Replacing: $msg\n";
    set_error_handler(function ($no, $msg) { echo "Replacement: $msg\n"; });
});
$d++;
$e++;
