<?php
// The SPL iterators compiled code implements natively: the engine's call
// order on the inner iterator, its refusals, and subclassing.
function t($f) {
    try {
        $r = $f();
        var_dump($r);
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

echo "== shapes\n";
var_dump(new EmptyIterator);
print_r(new IteratorIterator(new ArrayIterator([1])));
echo "\n";
var_dump(serialize(new IteratorIterator(new ArrayIterator([1]))), serialize(new EmptyIterator));
t(fn() => clone new IteratorIterator(new ArrayIterator([1])));
t(fn() => get_class(clone new EmptyIterator));
var_dump(new LimitIterator(new ArrayIterator([])) instanceof OuterIterator,
    new EmptyIterator instanceof OuterIterator, get_parent_class('CallbackFilterIterator'),
    method_exists('LimitIterator', 'getPosition'), method_exists('EmptyIterator', 'getPosition'),
    is_callable([new EmptyIterator, 'current']));

class L implements Iterator {
    private $i = 0;
    function __construct(private array $a) {}
    function rewind(): void { echo "rewind\n"; $this->i = 0; }
    function valid(): bool { echo "valid\n"; return $this->i < count($this->a); }
    function current(): mixed { echo "current\n"; return $this->a[$this->i]; }
    function key(): mixed { echo "key\n"; return $this->i; }
    function next(): void { echo "next\n"; $this->i++; }
}

echo "== IteratorIterator\n";
foreach (new IteratorIterator(new L([1, 2])) as $k => $v) echo "got $k=>$v\n";
echo "== LimitIterator, not seekable\n";
foreach (new LimitIterator(new L([1, 2, 3, 4]), 1, 2) as $k => $v) echo "got $k=>$v\n";
echo "== LimitIterator, seekable\n";
foreach (new LimitIterator(new ArrayIterator([1, 2, 3, 4]), 2) as $k => $v) echo "got $k=>$v\n";
echo "== CallbackFilterIterator\n";
$it = new CallbackFilterIterator(new L([1, 2, 3]), function ($c, $k, $i) {
    echo "cb $c $k ", get_class($i), "\n";
    return $c != 2;
});
foreach ($it as $k => $v) echo "got $k=>$v\n";
echo "== InfiniteIterator\n";
$n = 0;
foreach (new InfiniteIterator(new L([1, 2])) as $k => $v) {
    echo "got $k=>$v\n";
    if (++$n >= 3) break;
}

echo "== refusals\n";
$e = new EmptyIterator;
t(fn() => $e->current());
t(fn() => $e->key());
t(fn() => $e->valid());
t(fn() => $e->next());
t(fn() => $e->current(1));
t(fn() => new IteratorIterator());
t(fn() => new IteratorIterator(5));
t(fn() => get_class(new IteratorIterator(new stdClass)));
t(fn() => new LimitIterator(new ArrayIterator([]), -1));
t(fn() => new LimitIterator(new ArrayIterator([]), 0, -2));
t(fn() => new LimitIterator(new ArrayIterator([]), "x"));
t(fn() => new CallbackFilterIterator(new ArrayIterator([]), 'nope'));
t(fn() => new CallbackFilterIterator(new ArrayIterator([])));
t(fn() => new InfiniteIterator(new ArrayObject([])));
t(fn() => new FilterIterator(new ArrayIterator([])));
t(fn() => (new IteratorIterator(new ArrayIterator([])))->rewind(1));
t(fn() => (new IteratorIterator(new ArrayIterator([1])))->nope());
t(function () { foreach (new LimitIterator(new ArrayIterator([1, 2]), 0, 0) as $v) echo $v; return 'done'; });
t(function () { foreach (new LimitIterator(new ArrayIterator([1, 2]), 5) as $v) echo $v; return 'done'; });

echo "== LimitIterator seek\n";
$l = new LimitIterator(new ArrayIterator([1, 2, 3, 4, 5]), 1, 3);
t(fn() => $l->seek(0));
t(fn() => $l->seek(4));
t(fn() => $l->seek(2));
t(fn() => $l->current());
t(fn() => $l->getPosition());
t(fn() => $l->seek());
$ii = new IteratorIterator(new ArrayIterator([1]));
t(fn() => [$ii->current(), $ii->key(), $ii->valid()]);

echo "== aggregates and generators\n";
class A implements IteratorAggregate {
    function getIterator(): Iterator { echo "getIterator\n"; return new ArrayIterator([7]); }
}
$i = new IteratorIterator(new A);
var_dump(get_class($i->getInnerIterator()));
foreach ($i as $k => $v) echo "$k=>$v\n";
class GAgg implements IteratorAggregate {
    function getIterator(): Generator { yield 'k' => 'v'; }
}
t(fn() => iterator_to_array(new IteratorIterator(new GAgg)));
function g() { yield 'a' => 1; yield 'b' => 2; }
foreach (new IteratorIterator(g()) as $k => $v) echo "$k=>$v\n";
foreach (new LimitIterator(g(), 1) as $k => $v) echo "$k=>$v\n";
t(fn() => iterator_to_array(new LimitIterator(new ArrayIterator([1, 2, 3]), 1, 1)));
t(fn() => iterator_count(new InfiniteIterator(new EmptyIterator)));
t(fn() => new LimitIterator(iterator: new ArrayIterator([1, 2, 3]), limit: 1) instanceof LimitIterator);
$f = new CallbackFilterIterator(new ArrayIterator(['a' => 1, 'b' => 2]), fn($v) => $v > 1);
t(fn() => $f->accept());
$f->rewind();
t(fn() => $f->accept());
t(fn() => [$f->key(), $f->current()]);

echo "== subclasses\n";
class S extends IteratorIterator { function __construct() {} }
$s = new S;
t(fn() => $s->valid());
t(fn() => $s->getInnerIterator());
class U extends LimitIterator {
    function __construct() { parent::__construct(new ArrayIterator([9, 8]), 1); }
    function current(): mixed { return parent::current() * 10; }
}
foreach (new U as $k => $v) echo "$k=>$v\n";
class F extends FilterIterator {
    function accept(): bool { return $this->current() % 2 == 1; }
}
foreach (new F(new ArrayIterator([1, 2, 3])) as $k => $v) echo "F $k=>$v\n";

echo "== traces\n";
class T implements Iterator {
    function rewind(): void { throw new Exception("boom"); }
    function valid(): bool { return true; }
    function current(): mixed { return 1; }
    function key(): mixed { return 0; }
    function next(): void {}
}
try { foreach (new IteratorIterator(new T) as $x) {} } catch (Exception $e) { echo $e->getTraceAsString(), "\n"; }
try {
    $i = new CallbackFilterIterator(new ArrayIterator([1]), function ($v) { throw new Exception("cb"); });
    $i->rewind();
} catch (Exception $e) {
    echo $e->getTraceAsString(), "\n";
}
function gthrow() {
    yield 1;
    throw new Exception("gen");
}
try {
    foreach (new IteratorIterator(gthrow()) as $x) {}
} catch (Exception $e) {
    echo $e->getTraceAsString(), "\n";
}
