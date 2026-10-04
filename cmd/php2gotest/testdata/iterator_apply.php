<?php
// iterator_apply rewinds, tests and advances the iterator -- never current()
// or key() -- calls the callback with the same arguments each time, counts
// the calls and stops after a falsy answer.
class It implements Iterator {
    private $i = 0;
    function __construct(private $n) {}
    function rewind(): void { echo "rewind\n"; $this->i = 0; }
    function valid(): bool { echo "valid\n"; return $this->i < $this->n; }
    function current(): mixed { echo "current\n"; return $this->i; }
    function key(): mixed { echo "key\n"; return $this->i; }
    function next(): void { echo "next\n"; $this->i++; }
}
var_dump(iterator_apply(new It(3), function () { echo "cb\n"; return true; }));
var_dump(iterator_apply(new It(3), function ($a, $b) { echo "cb $a $b\n"; return true; }, [0, 5]));
var_dump(iterator_apply(new ArrayIterator([1, 2]), fn() => 1));
var_dump(iterator_apply(new ArrayIterator([1, 2]), fn() => null));
var_dump(iterator_apply(new ArrayIterator([1, 2, 3]), fn() => "0"));
var_dump(iterator_apply(new It(0), fn() => true));
function gen() { yield 1; yield 2; }
var_dump(iterator_apply(gen(), fn() => true));
class Agg implements IteratorAggregate {
    function getIterator(): Iterator { echo "getIterator\n"; return new ArrayIterator(['a', 'b']); }
}
var_dump(iterator_apply(new Agg, fn() => true));
// The iterator is passed in $args to read the element.
$it = new ArrayIterator(['x' => 10, 'y' => 20]);
var_dump(iterator_apply($it, function (Iterator $i) { echo $i->key(), '=', $i->current(), "\n"; return true; }, [$it]));
// A string key names the parameter.
var_dump(iterator_apply(new ArrayIterator([1]), function ($p = 0, $q = 0) { echo "p=$p q=$q\n"; return true; }, ['q' => 7]));
try {
    iterator_apply([1], fn() => true);
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
try {
    iterator_apply(new stdClass, fn() => true);
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
try {
    iterator_apply(new ArrayIterator([1]), fn() => true, "x");
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
try {
    iterator_apply(new ArrayIterator([1, 2]), function () { throw new Exception("E"); });
} catch (Exception $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
// Reached by name only.
$f = 'iterator_apply';
var_dump($f(new ArrayIterator([1, 2, 3]), fn() => true));
