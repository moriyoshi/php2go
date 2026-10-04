<?php
// Whole-program facts a lifetime claim depends on must be asked of every
// body the program can run -- static methods included -- and in the
// spelling the IR uses.
// Teardown order is not what this is about, so the destructors go quiet
// once the script ends.
class D {
    public static $quiet = false;
    function __construct(public string $n) {}
    function __destruct() { if (!self::$quiet) { echo "-{$this->n} "; } }
}

// A static method whose name an engine rule calls non-retaining stores its
// argument: the local handed to it must outlive its frame.
class R {
    public static $k = [];
    static function initializeLazyObject($o) { self::$k[] = $o; return $o; }
}
function lazyName() { $r = new R; $x = new D('x'); $r->initializeLazyObject($x); echo "run "; }
lazyName();
echo "| static method named like an engine one\n";

// The running Fiber stored by a function (a static method: see
// lifetime_fiber_static).
function keepFiber() { $GLOBALS['g'] = Fiber::getCurrent(); }
function startFibers() {
    $b = new Fiber(function () { keepFiber(); Fiber::suspend(); echo "resumed "; });
    $b->start();
    echo "started ";
}
startFibers();
echo "| fibers started\n";
$g->resume();
echo "| end\n";
D::$quiet = true;
