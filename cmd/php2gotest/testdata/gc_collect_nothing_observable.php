<?php
// gc_collect_cycles in programs where 0 and nothing done is exactly what the
// engine's collector amounts to: every object is still held -- by a global,
// a static, a suspended generator's frame, a running fiber, a closure's
// capture, a WeakMap whose key is alive -- so there is nothing to collect,
// and the count read back is 0. (Where something could be collectable and
// the program would notice, compiled code reports the gap instead.)
class Node {
    public static int $destroyed = 0;
    public $next;
    public function __construct(public string $name) {}
    public function __destruct() { Node::$destroyed++; }
}

// A cycle that is still reachable is not garbage.
$a = new Node('a');
$b = new Node('b');
$a->next = $b;
$b->next = $a;
echo "reachable cycle: ", gc_collect_cycles(), "\n";

// Held by a static property and by a closure's capture.
class Registry { public static $items = []; }
Registry::$items[] = new Node('static');
$keep = new Node('captured');
$fn = function () use ($keep) { return $keep->name; };
echo "static and capture: ", gc_collect_cycles(), " ", $fn(), "\n";

// A suspended generator's frame holds its locals.
$gen = (function () {
    $local = new Node('in generator');
    yield $local->name;
    yield 'second';
})();
echo "generator: ", $gen->current(), " ", gc_collect_cycles(), "\n";

// A WeakMap entry whose key is alive keeps its value.
$wm = new WeakMap();
$key = new Node('key');
$wm[$key] = new Node('value');
echo "weakmap: ", gc_collect_cycles(), " ", count($wm), "\n";

// Called from inside a running fiber: the fiber and what it holds are live.
$fiber = new Fiber(function () {
    $mine = new Node('in fiber');
    echo "inside fiber: ", gc_collect_cycles(), " ", $mine->name, "\n";
    Fiber::suspend();
});
$fiber->start();

// Held by nothing but the holder itself: a capture, a suspended generator's
// object local or array local, the closure a generator was made from, and a
// generator's argument.
function makeReader() { $o = new Node('capture only'); return function () use ($o) { return $o->name; }; }
$reader = makeReader();
echo "capture only: ", gc_collect_cycles(), " ", $reader(), "\n";
function objectLocal() { $n = new Node('object local'); yield $n->name; }
function arrayLocal() { $m = [new Node('array local')]; yield count($m); }
$g1 = objectLocal();
$g2 = arrayLocal();
echo "frame locals: ", $g1->current(), " ", $g2->current(), " ", gc_collect_cycles(), "\n";
$x = new Node('closure generator');
$g3 = (function () use ($x) { yield $x->name; })();
unset($x);
echo "closure generator: ", $g3->current(), " ", gc_collect_cycles(), "\n";
function withArg($a) { yield 1; yield $a->name; }
$g4 = withArg(new Node('argument'));
echo "argument: ", $g4->current(), " ", gc_collect_cycles(), "\n";

// Held by the callbacks the request keeps: an error handler, an exception
// handler, an autoloader and a shutdown function, each the only holder.
function installHandlers() {
    $e = new Node('error handler');
    set_error_handler(function () use ($e) { return false; });
    $x = new Node('exception handler');
    set_exception_handler(function ($t) use ($x) { echo $x->name, "\n"; });
    $a = new Node('autoloader');
    spl_autoload_register(function ($c) use ($a) {});
    $s = new Node('shutdown');
    register_shutdown_function(function () use ($s) { echo "shutdown ", $s->name, "\n"; });
}
installHandlers();
echo "callbacks: ", gc_collect_cycles(), "\n";

// A discarded call in a program with nothing to observe.
gc_collect_cycles();
echo "destroyed so far: ", Node::$destroyed, "\n";
echo "done\n";
