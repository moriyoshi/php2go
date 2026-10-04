<?php
// `__destruct` is never reached through `__call`. A class with a `__call` and
// no destructor has nothing to run at destruction -- but a dispatcher that
// falls through to `__call` for any absent name will happily call the accessor
// with the name "__destruct", which prints where PHP prints nothing.
class Caller {
    public function __call($name, $args) { echo "call:$name\n"; }
    public static function __callStatic($name, $args) { echo "static:$name\n"; }
}
class Plain extends Caller {}

function inFrame() { $x = new Plain(); echo "in frame\n"; }
inFrame();
echo "after frame\n";

// A bare statement: destroyed at once, still no __call.
new Plain();
echo "after bare\n";

// An inherited *real* destructor must still run.
class Noisy extends Caller { public function __destruct() { echo "real dtor\n"; } }
class Inherits extends Noisy {}
function inFrame2() { $y = new Inherits(); echo "in frame2\n"; }
inFrame2();
echo "after frame2\n";

// __call still answers for an ordinary absent method.
$p = new Plain();
$p->whatever();
var_dump(method_exists($p, '__destruct'));
echo "end\n";
