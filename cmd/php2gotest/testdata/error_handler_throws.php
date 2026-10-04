<?php
// A `set_error_handler` callback may **throw**, and the exception has to leave
// the operation that raised the diagnostic.
//
// A diagnostic is raised from places that cannot unwind -- deep inside the
// value model -- so the throw is parked and collected at the next instruction
// boundary. Compiled code collected it nowhere, so every one of these was
// silently swallowed: the handler ran, the exception vanished, and the program
// carried on with a null.

set_error_handler(function (int $errno, string $errstr) {
    throw new \ErrorException($errstr, 0, $errno);
});

try {
    echo $undefinedVariable;
} catch (\ErrorException $e) {
    echo 'read: ', $e->getMessage(), "\n";
}

$a = [];
try {
    echo $a['missing'];
} catch (\ErrorException $e) {
    echo 'key: ', $e->getMessage(), "\n";
}

try {
    $x = 1 + 'abc';
} catch (\ErrorException $e) {
    echo 'arith: ', $e->getMessage(), "\n";
}

$o = new stdClass;
try {
    echo $o->absent;
} catch (\ErrorException $e) {
    echo 'property: ', $e->getMessage(), "\n";
}

#[\Deprecated]
function old() {}
try {
    old();
} catch (\ErrorException $e) {
    echo 'deprecation: ', $e->getMessage(), "\n";
}

// A handler that does *not* throw still lets the program continue, and the
// default text stays suppressed because the handler handled it.
set_error_handler(function (int $errno, string $errstr) {
    echo 'handled: ', $errstr, "\n";
    return true;
});
echo $stillUndefined;
echo "done\n";
