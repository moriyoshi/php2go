<?php
// A dual SPL iterator hands a method it does not have to the iterator it
// wraps (spl_dual_it_get_method): calls, method_exists and is_callable.
class In implements Iterator {
    function rewind(): void {}
    function valid(): bool { return false; }
    function current(): mixed { return 1; }
    function key(): mixed { return 0; }
    function next(): void {}
    function hello($x) {
        echo "hello $x from ", get_class($this), "\n";
        throw new Exception("e");
    }
    private function priv() {}
    static function st() { echo "static\n"; }
}
$it = new IteratorIterator(new In);
var_dump(method_exists($it, 'hello'), method_exists('IteratorIterator', 'hello'),
    is_callable([$it, 'hello']), method_exists($it, 'priv'), is_callable([$it, 'priv']),
    method_exists($it, 'nope'), is_callable([$it, 'nope']));
try { $it->hello(5); } catch (Exception $e) { echo $e->getTraceAsString(), "\n"; }
$it->st();
try { $it->nope(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$l = new LimitIterator(new ArrayIterator([1, 2]));
var_dump($l->count(), $l->offsetGet(1));
class CIt extends IteratorIterator {
    function __call($n, $a) { return "own call $n"; }
}
$c = new CIt(new ArrayIterator([4]));
var_dump($c->count());
var_dump((new EmptyIterator) instanceof Countable);
try { (new MultipleIterator)->count(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$it = new IteratorIterator(new ArrayIterator([1, 2, 3]));
foreach ($it as $v) {
    if (method_exists($it, 'offsetGet')) {
        var_dump($it->offsetGet(0));
    }
}
