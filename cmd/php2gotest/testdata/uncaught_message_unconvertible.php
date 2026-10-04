<?php
// Nothing catches the exception, so the engine renders it through
// __toString where no script is running. Its own message is an array (a
// warning, at `Unknown` line 0), and its cause's is an object with no string
// form: that throws, and the Error is what is reported, raised in
// `[no active file]` from the `[internal function]` frame.
set_error_handler(function ($no, $message, $file, $line) {
    echo "handler: $message at $file:$line\n";
    return true;
});
register_shutdown_function(function () {
    $last = error_get_last();
    echo "last: ", strtok($last["message"], "\n"), "\n";
});
class WithMessage extends Exception {
    function __construct($message, $previous = null) { parent::__construct("", 0, $previous); $this->message = $message; }
}
throw new WithMessage([1], new WithMessage(new stdClass));
