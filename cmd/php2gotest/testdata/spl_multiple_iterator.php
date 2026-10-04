<?php
// MultipleIterator: flags, strict valid(), keyed results, and its dump.
function t($f) {
    try {
        $r = $f();
        var_dump($r);
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
$d = new MultipleIterator(MultipleIterator::MIT_KEYS_ASSOC);
$d->attachIterator(new ArrayIterator([1]), 'a');
$emp = new EmptyIterator;
$d->attachIterator($emp);
var_dump($d);
print_r($d);
echo "\n";
var_dump((array)$d, count(get_object_vars($d)));
$c = clone $d;
$c->detachIterator($emp);
var_dump($c->countIterators(), $d->countIterators());
$m = new MultipleIterator;
var_dump($m->getFlags(), $m->countIterators(), $m->valid());
t(fn() => $m->current());
t(fn() => $m->key());
$a = new ArrayIterator([1, 2]);
$b = new ArrayIterator(['x' => 3]);
$m->attachIterator($a);
$m->attachIterator($b);
foreach ($m as $k => $v) { var_dump($k, $v); }
$m->rewind();
$m->next();
var_dump($m->valid());
t(fn() => $m->current());
t(fn() => $m->key());
$m2 = new MultipleIterator(MultipleIterator::MIT_NEED_ANY | MultipleIterator::MIT_KEYS_ASSOC);
$m2->attachIterator($a, 'A');
$m2->attachIterator($b, 'B');
foreach ($m2 as $k => $v) { var_dump($k, $v); }
t(fn() => $m2->attachIterator(new ArrayIterator([])));
t(fn() => $m2->attachIterator(new ArrayIterator([]), 'A'));
t(fn() => $m2->attachIterator(new ArrayIterator([]), []));
t(fn() => $m2->attachIterator([]));
var_dump($m2->containsIterator($a), $m2->countIterators());
$m2->detachIterator($a);
var_dump($m2->containsIterator($a), $m2->countIterators());
var_dump(MultipleIterator::MIT_NEED_ANY, MultipleIterator::MIT_NEED_ALL,
    MultipleIterator::MIT_KEYS_NUMERIC, MultipleIterator::MIT_KEYS_ASSOC);
$m3 = new MultipleIterator(MultipleIterator::MIT_KEYS_ASSOC);
$m3->attachIterator(new ArrayIterator([1]));
t(fn() => $m3->current());
$m3->setFlags(0);
var_dump($m3->getFlags());

// valid() must answer **true itself**: a truthy 1 is not enough.
class Truthy implements Iterator {
    function rewind(): void {}
    #[\ReturnTypeWillChange]
    function valid() { return 1; }
    function current(): mixed { return 'c'; }
    function key(): mixed { return 'k'; }
    function next(): void {}
}
$m4 = new MultipleIterator;
$m4->attachIterator(new Truthy);
var_dump($m4->valid());
$m5 = new MultipleIterator(MultipleIterator::MIT_NEED_ANY);
$m5->attachIterator(new Truthy);
var_dump($m5->valid());

function gen1() { yield 'a'; yield 'aa'; }
function gen2() { yield 'b'; yield 'bb'; }
$it = new MultipleIterator;
$it->attachIterator(gen1());
$it->attachIterator(gen2());
foreach ($it as $values) { var_dump($values); }

