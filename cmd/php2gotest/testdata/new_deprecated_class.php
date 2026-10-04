<?php
// The `#[\Deprecated]` attribute's class built with `new`: `?string
// $message` and `$since`, coerced as an internal function coerces, both
// readonly -- written again, or through the constructor called again, it
// is `Cannot modify readonly property Deprecated::$message`
// (attributes/deprecated/property_readonly_*).

var_dump(new Deprecated("msg", "8.4"));
var_dump(new Deprecated());
var_dump(new Deprecated(since: "1"));
var_dump(new Deprecated(1234));
foreach ([
    fn() => new Deprecated([]),
    function () { $d = new Deprecated("m"); $d->message = "x"; },
    function () { $d = new Deprecated("m"); $d->__construct("again"); },
] as $f) {
    try {
        $f();
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
