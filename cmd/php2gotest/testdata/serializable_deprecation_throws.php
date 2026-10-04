<?php
// A handler that throws on the Serializable deprecation does not throw out of
// the declaration: linking the class fails, and the engine reports the whole
// chain as the link's fatal error, at the declaration's line (gh15907).
// Neither the catch nor the finally around it runs; shutdown functions do.

register_shutdown_function(function () { echo "shutdown\n"; });
set_error_handler(function ($no, $msg) {
    echo "handler\n";
    throw new LogicException("wrapped", 3, new RuntimeException("inner"));
});
try {
    echo "before\n";
    class D implements Serializable {
        public function serialize() {}
        public function unserialize($s) {}
    }
} catch (Throwable $t) {
    echo "caught ", get_class($t), "\n";
} finally {
    echo "finally\n";
}
echo "after\n";
