<?php
// After a fatal error the engine destroys nothing -- no destructor, no
// suspended generator's or fiber's finally -- and still runs the shutdown
// functions.
class D {
    function __destruct() {
        echo "destructor\n";
    }
}
$d = new D;
$f = new Fiber(function () {
    try {
        Fiber::suspend();
    } finally {
        echo "fiber finally\n";
    }
});
$f->start();
register_shutdown_function(function () {
    echo "shutdown\n";
});
@trigger_error("fatal", E_USER_ERROR);
