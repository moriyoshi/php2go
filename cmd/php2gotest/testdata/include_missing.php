<?php
// An include of a file that is not there warns and answers false; a
// require warns and throws.
var_dump(include 'include_missing_nothing_here.inc');
var_dump(include_once 'include_missing_nothing_here.inc');
try {
    require_once 'include_missing_nothing_here.inc';
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
set_error_handler(function ($no, $msg) {
    echo "handler: $msg\n";
    throw new Exception("from the handler");
});
try {
    require 'include_missing_nothing_here.inc';
} catch (Exception $e) {
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
}
