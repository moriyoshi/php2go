<?php
// Temporaries in bodies that suspend. One consumed by a call or a method dies
// there, before or after a suspension; one that lives across a suspension
// inside the call's own argument list -- `take(new D, inner())`, or
// `take(new D, yield)` in a generator -- is carried in a compiler slot and dies
// when the call returns; a `foreach` over a fresh object dies at the loop's end
// even when the body suspends; an object handed to a suspending call that
// keeps nothing dies with the frame. Negative: a call that keeps its argument.
class D {
    function __construct(public $n) {}
    function __destruct() { echo "D{$this->n} gone\n"; }
    function hi() { return "hi {$this->n}"; }
}
class P {
    public $a = 1;
    public $b = 2;
    function __construct(public $n) {}
    function __destruct() { echo "P{$this->n} gone\n"; }
}
function inner($x = null) { return Fiber::suspend('s'); }
function take($o, $v) { echo "take {$o->n} $v\n"; }
class Box { public static $s = []; }
function stash($o, $v) { Box::$s[] = $o; echo "stash {$o->n} $v\n"; }
function body() {
    var_dump((new D(1))->n);
    echo "after temp\n";
    inner();
    var_dump((new D(2))->n);
    echo "after temp past a suspension\n";
    take(new D(3), inner());
    echo "after take across a suspension\n";
    stash(new D(4), inner());
    echo "after stash across a suspension\n";
    foreach (new P(5) as $k => $v) {
        inner();
        echo "prop $k\n";
    }
    echo "after loop\n";
    $o = new D(6);
    inner($o);
    echo "returning\n";
}
$f = new Fiber('body');
$f->start();
while (!$f->isTerminated()) {
    $f->resume();
}
echo "fiber done\n";
function gen() {
    take(new D(10), yield 1);
    echo "gen after take across a yield\n";
    take(new D(11), yield 2);
    echo "gen after a second take across a yield\n";
}
foreach (gen() as $v) {
    echo "got $v\n";
}
echo "end\n";
