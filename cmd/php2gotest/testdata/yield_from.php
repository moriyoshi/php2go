<?php
// `yield from` delegates to an array, a generator or a Traversable. The whole
// protocol lived in runtime/php and the lowering emitted the terminator; only
// the emitter's one line handing the source over was missing, so every
// `yield from` refused to compile.

// Keys are the **delegate's own**, which is why `yield from` over two arrays
// repeats keys 0,1 rather than continuing a count.
function overArrays() {
    yield "first";
    yield from ["a", "b"];
    yield from ["c"];
    yield "last";
}
foreach (overArrays() as $k => $v) { echo "  arr $k=$v\n"; }

function inner() { yield 1; yield 2; return "inner-return"; }
// The **value of the expression** is the delegate's return, not its last
// element.
function outer() {
    $got = yield from inner();
    yield "got:" . $got;
}
foreach (outer() as $k => $v) { echo "  gen $k=$v\n"; }

class Two implements Iterator {
    private $i = 0;
    public function rewind(): void { echo "rewind\n"; $this->i = 0; }
    public function valid(): bool { return $this->i < 2; }
    public function current(): mixed { return "t{$this->i}"; }
    public function key(): mixed { return "k{$this->i}"; }
    public function next(): void { $this->i = $this->i + 1; }
}
function overTraversable() { yield from new Two(); yield "after"; }
foreach (overTraversable() as $k => $v) { echo "  trav $k=$v\n"; }

// Nested delegation.
function a() { yield 1; }
function b() { yield from a(); yield 2; }
function c() { yield from b(); yield 3; }
foreach (c() as $k => $v) { echo "  nest $k=$v\n"; }

// `yield from` a non-Traversable object is the **program** being wrong, not a
// capability we lack: PHP throws. The two answers looked alike while the
// resolver hop was uninstalled, because "no hop" and "not Traversable" were
// both a bare false.
function bad() { yield from new stdClass(); }
try { bad()->current(); } catch (Error $e) { echo "caught: ", $e->getMessage(), "\n"; }

// A delegate is **copied**, not read through. Two generators can delegate to
// the same one, and it can be advanced directly besides -- and what this
// generator yields is what it took when it took it.
function one() { yield 1; }
function via($g) { yield from $g; echo "reached\n"; yield 2; }
$g1 = one();
$g2 = via($g1);
var_dump($g2->valid(), $g2->current());
$g1->next();
var_dump($g1->valid(), $g2->valid(), $g2->current());
$g2->next();
var_dump($g2->valid(), $g2->current());

// Throwing into a delegating generator: the delegate gets it first, and if it
// does not handle it the exception continues into the *delegating* body, where
// a try/catch around the `yield from` may catch it.
function innerNoCatch() { yield "i"; }
function outerCatches() {
    try { yield from innerNoCatch(); } catch (Exception $e) { echo "outer caught: ", $e->getMessage(), "\n"; }
    yield "after";
}
$o = outerCatches();
var_dump($o->current());
var_dump($o->throw(new Exception("boom")));
