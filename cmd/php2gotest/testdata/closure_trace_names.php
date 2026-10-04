<?php
// A trace names a closure by its **own** name, `{closure:FILE:LINE}`, prefixed
// with the class it carries: a bound receiver gives `Host->{closure:...}` and a
// scope with no receiver -- a closure made inside a static method -- gives
// `Host::{closure:...}`. php2go pushed the literal `{closure}` for all three.

class Host {
    public function make() { return function () { throw new Exception("x"); }; }
    public static function smake() { return function () { throw new Exception("y"); }; }
}
$h = new Host;
$f = $h->make();
try { $f(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n"; }
$g = Host::smake();
try { $g(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n"; }
$t = function () { throw new Exception("z"); };
try { $t(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n"; }
try { call_user_func($t); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n"; }

// An error handler is a closure reached as a callback, and gets the same name.
set_error_handler(function ($no, $msg) { throw new Exception("handled: $msg"); });
function boom() { echo $undefined; }
try { boom(); } catch (Throwable $e) { echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n"; }
restore_error_handler();
