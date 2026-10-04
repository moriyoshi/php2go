<?php
// An uncaught exception is printed through its class's own __toString: what
// it returns follows `Uncaught`, with no trace of the engine's.
register_shutdown_function(function () {
    $last = error_get_last();
    var_dump($last["message"]);
});
class Custom extends RuntimeException {
    function __toString(): string { return "custom <" . $this->getMessage() . ">"; }
}
function fail() { throw new Custom("boom"); }
fail();
