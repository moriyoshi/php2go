<?php
// call_user_func checks its callable before the call: an exception thrown
// during the check -- by the autoloader, or by an error handler given the
// `self::` deprecation -- becomes the previous of a TypeError, whose
// reason is the check's, or "(null)" when the callable was fine. array_map
// lets the handler's exception through as it is.
function show(Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), " @ ", $e->getLine(), "\n";
    if ($p = $e->getPrevious()) {
        echo "  previous ", get_class($p), ": ", $p->getMessage(), "\n";
    }
}
spl_autoload_register(function ($c) {
    throw new Exception("autoload $c");
});
try {
    call_user_func("Missing::m");
} catch (Throwable $e) {
    show($e);
}
set_error_handler(function ($no, $msg) {
    throw new Exception("handler: $msg");
});
class K {
    static function s() { return 1; }
    function t() {
        try {
            call_user_func("self::s");
        } catch (Throwable $e) {
            show($e);
        }
        try {
            call_user_func_array(["static", "s"], []);
        } catch (Throwable $e) {
            show($e);
        }
        try {
            array_map("self::s", [1]);
        } catch (Throwable $e) {
            show($e);
        }
    }
}
(new K)->t();

// A handler that returns is told once, by the check, not again by the call.
set_error_handler(function ($no, $msg) {
    echo "told: $msg\n";
    return true;
});
class L {
    static function s() { return "called"; }
    function t() {
        var_dump(call_user_func("self::s"), call_user_func("static::s"));
    }
}
(new L)->t();
