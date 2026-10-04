<?php
// A function a fiber drives is promoted (it suspends only through calls), so
// its locals are SSA values, and one live across a suspension is demoted to a
// CPS slot. The slot is that local's own storage: its object still dies at the
// overwrite, inside a loop and after a method call on it. Two locals holding
// one object is a different matter -- `$q = $p` -- and `$p = null` must leave
// the object to `$q` (this released it early, even in a plain function).
class D {
    function __construct(public $n) {}
    function __destruct() { echo "D{$this->n} gone\n"; }
    function hi() { return "hi {$this->n}"; }
}
function inner() { return Fiber::suspend('s'); }
function body() {
    $e = new D(1);
    inner();
    echo $e->hi(), "\n";
    $e = new D(2);
    echo "e overwritten\n";
    for ($i = 0; $i < 3; $i++) {
        $o = new D(10 + $i);
        inner();
        echo $o->hi(), "\n";
    }
    echo "loop done\n";
    $o = null;
    echo "o nulled\n";
    $p = new D(20);
    $q = $p;
    inner();
    $p = null;
    echo "p nulled, q holds\n";
    return $q;
}
function plain() {
    $p = new D(30);
    $q = $p;
    $p = null;
    echo "plain: p nulled, q holds\n";
    return $q;
}
$f = new Fiber('body');
$f->start();
while (!$f->isTerminated()) {
    $f->resume();
}
$kept = $f->getReturn();
$kept2 = plain();
// Two locals, and the fiber never finishes: `$q` holds the object until the
// suspended fiber is destroyed at the end of the request.
$g = new Fiber(function () {
    $p = new D(40);
    $q = $p;
    $p = null;
    echo "fiber: p nulled, q holds\n";
    inner();
    echo "never\n";
});
$g->start();
echo "end\n";
