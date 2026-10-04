<?php
// A generator's locals die when its body ends -- a return, a throw, or the
// close a destroyed generator runs -- and not before; a destructor they run
// traces from the line that drove the generator, or under a `yield from`
// from the line that drove the outermost one.

class D {
    public static bool $loud = true;
    public function __construct(public string $n) {}
    public function __destruct() {
        if (!self::$loud) {
            return;
        }
        echo "~{$this->n}\n";
        debug_print_backtrace();
    }
}

$kept = [];

function keep($o) {
    global $kept;
    $kept[] = $o;
}

// Ends by running off its end, under a foreach.
function ends() {
    $d = new D("ends");
    echo "ends: before\n";
    yield 1;
    echo "ends: after\n";
}
foreach (ends() as $v) {
    echo "got $v\n";
}
echo "after ends\n";

// Destroyed while suspended: its finally runs first, then its locals go.
function closed() {
    $d = new D("closed");
    try {
        yield 1;
    } finally {
        echo "closed: finally\n";
    }
}
$g = closed();
$g->current();
echo "unset\n";
unset($g);
echo "after unset\n";

// Ends by throwing: the local dies before the catch runs.
function throws() {
    $d = new D("throws");
    yield 1;
    throw new Exception("boom");
}
try {
    foreach (throws() as $v) {
        echo "throws: $v\n";
    }
} catch (Exception $e) {
    echo "caught ", $e->getMessage(), "\n";
}

// A `yield from` chain: each level's local dies as that level ends.
function outer() {
    $d = new D("outer");
    yield 0;
    yield from middle();
    echo "outer: after\n";
    yield 9;
}
function middle() {
    $d = new D("middle");
    yield from leaf();
    echo "middle: after\n";
}
function leaf() {
    $d = new D("leaf");
    yield 1;
    echo "leaf: end\n";
}
foreach (outer() as $v) {
    echo "chain $v\n";
}

// A delegate whose body ends on its first run, as the `yield from` starts it.
function quick() {
    $d = new D("quick");
    return 5;
    yield;
}
function wraps() {
    yield 0;
    $r = yield from quick();
    echo "wraps got $r\n";
    yield 1;
}
foreach (wraps() as $v) {
    echo "wraps $v\n";
}

// Driven by ->next(): the driver is the engine's.
function viaNext() {
    yield from viaNextLeaf();
}
function viaNextLeaf() {
    $d = new D("next");
    yield 1;
}
$n = viaNext();
$n->current();
$n->next();
echo "after next\n";

// An Iterator handed to `yield from` is let go of when the delegation ends,
// and dies with the frame; its current() throwing is caught in the body.
class It implements Iterator {
    private int $i = 0;
    public function __construct(private string $n, private int $failAt) {}
    public function current(): mixed {
        if ($this->i === $this->failAt) {
            throw new Exception("current at {$this->i}");
        }
        return $this->i;
    }
    public function key(): mixed { return $this->i; }
    public function next(): void { $this->i++; }
    public function rewind(): void { $this->i = 0; }
    public function valid(): bool { return $this->i < 3; }
    public function __destruct() { echo "~It {$this->n}\n"; }
}
function delegates($failAt) {
    try {
        $it = new It("f$failAt", $failAt);
        yield from $it;
        echo "delegates: spent\n";
    } catch (Exception $e) {
        echo "delegates: ", $e->getMessage(), "\n";
    }
}
foreach ([0, 1, 5] as $failAt) {
    foreach (delegates($failAt) as $k => $v) {
        echo "$k => $v\n";
    }
    echo "done $failAt\n";
}

// **Must survive the body.** A value the body hands out, returns, stores,
// captures or gives to a retaining call is not the frame's.
function yields() {
    $d = new D("yielded");
    yield $d;
}
foreach (yields() as $o) {
    $held = $o;
}
echo "after yields: {$held->n}\n";

function returns() {
    $d = new D("returned");
    yield 1;
    return $d;
}
$r = returns();
foreach ($r as $v) {}
$back = $r->getReturn();
echo "after returns: {$back->n}\n";

class Box {
    public $slot;
    public function fill() {
        $d = new D("stored");
        $this->slot = $d;
        yield 1;
    }
}
$box = new Box();
foreach ($box->fill() as $v) {}
echo "after stores: {$box->slot->n}\n";

function captures() {
    $d = new D("captured");
    $f = function () use ($d) { return $d->n; };
    yield $f;
}
foreach (captures() as $f) {
    $cl = $f;
}
echo "after captures: ", $cl(), "\n";

function passes() {
    $d = new D("passed");
    keep($d);
    yield 1;
}
foreach (passes() as $v) {}
echo "after passes: {$kept[0]->n}\n";

// An IteratorAggregate: the delegation calls getIterator(), which may hand
// the object anywhere -- this one keeps it.
class Agg implements IteratorAggregate {
    public static $kept = [];
    public function getIterator(): Iterator {
        self::$kept[] = $this;
        return new ArrayIterator([10, 20]);
    }
    public function __destruct() {
        if (D::$loud) {
            echo "~Agg\n";
        }
    }
}
function aggregates() {
    $a = new Agg();
    yield from $a;
}
foreach (aggregates() as $k => $v) {
    echo "$k => $v\n";
}
echo "after aggregates: ", count(Agg::$kept), "\n";
// Survivors say nothing at script end: the order objects are torn down
// there is a separate question from this one.
D::$loud = false;
echo "end\n";
