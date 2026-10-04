<?php
// A parameter typed with an engine exception class or interface accepts the
// engine's own exceptions and subclasses of them.
function takesThrowable(\Throwable $e) { return get_class($e); }
function takesException(Exception $e) { return get_class($e); }
function takesError(Error $e) { return get_class($e); }
class MyRuntime extends RuntimeException {}
var_dump(takesThrowable(new Exception("x")), takesThrowable(new TypeError("t")));
var_dump(takesException(new LogicException("l")), takesException(new MyRuntime("m")));
var_dump(takesError(new ArgumentCountError("a")));
try {
    takesException(new Error("wrong family"));
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
$closure = function (\Throwable $e): string { return "closure took " . get_class($e); };
var_dump($closure(new DivisionByZeroError("d")));

// A throw from a shutdown function reaches the exception handler.
set_exception_handler(function (\Throwable $exception) {
    echo 'Caught: ' . $exception->getMessage() . "\n";
});
register_shutdown_function(function () {
    echo "shutdown\n";
    throw new \Exception('from shutdown');
});
register_shutdown_function(function () {
    echo "second shutdown\n";
});
