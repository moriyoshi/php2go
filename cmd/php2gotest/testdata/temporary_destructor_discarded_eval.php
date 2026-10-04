<?php
// A bare `new` statement is dead once its constructor returns, and its
// destructor runs there -- in a body that also runs eval, include or
// extract, none of which can name an object no variable holds. The
// destructor's deprecation, turned into an exception by the handler, is
// caught around the statement (attributes/deprecated/functions/
// throwing_error_handler_001).

set_error_handler(function (int $errno, string $errstr) {
    throw new ErrorException($errstr, 0, $errno);
});

class Noisy {
    public function __destruct() { echo "dtor Noisy\n"; }
}

class DeprecatedDtor {
    #[\Deprecated("do not destroy")]
    public function __destruct() { echo "not executed\n"; }
}

new Noisy();
echo "after Noisy\n";

try {
    new DeprecatedDtor();
} catch (ErrorException $e) {
    echo "caught: ", $e->getMessage(), "\n";
}

$vars = ['x' => 1];
extract($vars);
eval('echo "eval sees x=$x\n";');
echo "end\n";
