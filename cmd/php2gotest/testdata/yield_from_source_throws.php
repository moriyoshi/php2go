<?php
// An exception from the source of a `yield from` -- its valid() on the first
// look, or an inner generator throwing while being advanced -- is thrown into
// the delegating generator at the `yield from`, where a try catches it.

class Broken implements Iterator
{
    function rewind(): void {}
    function current(): mixed { return 1; }
    function key(): mixed { return 0; }
    function next(): void {}
    function valid(): bool { throw new Exception("from valid()"); }
}
function outer()
{
    try {
        var_dump(yield from new Broken);
    } catch (Exception $e) {
        echo "caught: ", $e->getMessage(), "\n";
    }
    yield "after";
}
foreach (outer() as $v) {
    var_dump($v);
}

function inner() { yield 1; throw new LogicException("inner"); }
function delegating()
{
    try {
        yield from inner();
    } catch (LogicException $e) {
        echo "caught: ", $e->getMessage(), "\n";
        yield 2;
    }
}
foreach (delegating() as $v) {
    var_dump($v);
}
