<?php
namespace App;

// An engine constant the engine flags as deprecated -- E_STRICT since 8.4 --
// reports at every use, including when a namespaced lookup falls back to it,
// and the error handler sees the notice.

var_dump(\E_STRICT);
var_dump(E_STRICT);
set_error_handler(function ($no, $msg) {
    throw new \ErrorException($msg, 0, $no);
});
try {
    $x = E_STRICT;
} catch (\ErrorException $e) {
    echo "caught: ", $e->getMessage(), "\n";
}
var_dump(E_ALL, \PHP_INT_SIZE);
