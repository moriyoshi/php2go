<?php
// Releases inside a body a fiber drives: it makes calls that may suspend, so
// it is a state machine whose locals are frame fields. An object a local owns
// outright still dies at the `unset` or the overwrite, before or after a
// suspension, and the rest of the frame's objects die when the body returns
// -- not at the end of the request. A destroyed fiber's frame dies at its
// destruction. Objects something else still holds are left alone.
class D {
    function __construct(public $n) {}
    function __destruct() { echo "D{$this->n} gone\n"; }
}
class Holder { public static $held = []; }
function inner($keep = null) {
    if ($keep) Holder::$held[] = $keep;
    return Fiber::suspend('s');
}
function body() {
    $a = new D(1); unset($a); echo "after unset\n";
    $b = new D(2); $b = null; echo "after overwrite\n";
    inner();
    $e = new D(3); unset($e); echo "after unset past a suspension\n";
    $k = new D(4);
    inner();
    unset($k); echo "after unset across a suspension\n";
    // Negative: handed to a call that keeps it.
    $kept = new D(5);
    inner($kept);
    unset($kept); echo "after unset of a kept one\n";
    $m = new D(7);
    echo "returning\n";
}
$f = new Fiber('body');
$f->start();
while (!$f->isTerminated()) {
    echo "-- resume\n";
    $f->resume();
}
echo "fiber done\n";
// Destroyed while suspended: its locals die with it.
$f2 = new Fiber(function () {
    $z = new D(8);
    inner();
    echo "never\n";
});
$f2->start();
unset($f2);
echo "f2 unset\n";
