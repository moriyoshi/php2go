<?php
// `foreach ($it as &$v)` over an ArrayIterator binds the loop variable to the
// iterator's storage: the writes land in its array, in the object it was built
// over, or -- through getIterator() -- in an ArrayObject. A typed property keeps
// its type through the box, a readonly one refuses the reference, a private one
// is never reached, and any other Iterator is refused before rewind().

// First, so the object's handle is #1 whatever later objects' lifetimes are:
// the `&` shows while `$v` still holds the property.
class Agg implements IteratorAggregate {
    public string $foo = 'bar';
    public function getIterator(): Iterator { echo "getIterator\n"; return new ArrayIterator($this); }
}
$o = new Agg;
foreach ($o as $k => &$v) {
    try { $v = []; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
    $v = 42;
}
var_dump($o);
unset($v);

$it = new ArrayIterator([1, 2]);
foreach ($it as &$v) { $v *= 10; }
unset($v);
var_dump($it->getArrayCopy());

$src = [1, 2];
$it = new ArrayIterator($src);
foreach ($it as &$v) { $v = 4; }
unset($v);
var_dump($src, $it->getArrayCopy());

$it = new ArrayIterator(['a' => 1, 'b' => 2]);
foreach ($it as $k => &$v) {
    // The slot is bound while the body runs: the write is read straight back.
    $v = "$k!";
    var_dump($it[$k]);
}
unset($v);

$ao = new ArrayObject([1, 2]);
foreach ($ao as &$v) { $v = 9; }
unset($v);
var_dump($ao->getArrayCopy());
$iter = $ao->getIterator();
$iter[0] = 7;
var_dump($ao[0]);

class P { public $x = 1; public int $t = 2; private int $p = 3; }
$p = new P;
foreach (new ArrayIterator($p) as $k => &$v) { echo "key $k\n"; $v = 7; }
unset($v);
var_dump($p->x, $p->t);

class Copy implements IteratorAggregate {
    public $a = [1, 2];
    public function getIterator(): Iterator { return new ArrayIterator($this->a); }
}
$c = new Copy;
foreach ($c as &$v) { $v = 9; }
unset($v);
var_dump($c->a);

class R { public function __construct(public readonly int $r = 1) {} }
$r = new R;
try {
    foreach (new ArrayIterator($r) as &$v) { echo "body\n"; }
} catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
unset($v);

class Sub extends ArrayIterator {
    public function current(): mixed { echo "current\n"; return parent::current(); }
}
try {
    foreach (new Sub([1]) as &$v) { echo "body\n"; }
} catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

class U implements Iterator {
    private $i = 0;
    public function current(): mixed { return $this->i; }
    public function key(): mixed { return $this->i; }
    public function next(): void { $this->i++; }
    public function rewind(): void { echo "rewind\n"; $this->i = 0; }
    public function valid(): bool { return $this->i < 2; }
}
try {
    foreach (new U as &$v) { echo "body\n"; }
} catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
