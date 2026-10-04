<?php
// A `Fiber::suspend()` inside a generator reached through `yield from` has to
// travel out through **two** hops: the delegating generator, and whatever the
// delegate turned out to be.
//
// `yield from new It()` over an `IteratorAggregate` resolves to a Traversable
// wrapping what `getIterator()` returned -- a Generator here -- so the body that
// parks is not the one being driven, and finding it means following the chain.
// `runtime/php` cannot name the wrapper, so it asks through an interface the
// emitted code answers.

class It implements IteratorAggregate {
    public function getIterator(): Generator {
        yield 'foo';
        $got = Fiber::suspend('from getIterator');
        yield "after:$got";
    }
}

function f() { yield from new It(); }

$iterable = f();
$fiber = new Fiber(function () use ($iterable) {
    var_dump($iterable->current());
    $iterable->next();
    // **The element after the resume.** This is what tells a chain that really
    // continued from one that only looked like it did: every generator in the
    // chain is parked, so resuming the *outermost* hands the value to a body
    // sitting at its `yield from` and the chain answers the stale element.
    var_dump($iterable->current());
    return "done";
});
var_dump($fiber->start());
var_dump($fiber->resume("R"));
var_dump($fiber->getReturn());

// Delegating to a plain generator takes the other branch of the same walk.
function inner2() { yield 1; $g = Fiber::suspend("inner2"); yield "i2:$g"; }
function outer2() { yield from inner2(); }
$i2 = outer2();
$f2 = new Fiber(function () use ($i2) {
    var_dump($i2->current());
    $i2->next();
    var_dump($i2->current());
});
var_dump($f2->start());
var_dump($f2->resume("S"));

// `yield from` over an array drives no generator at all, and is untouched.
function overArray() { yield from [1, 2]; yield 3; }
foreach (overArray() as $v) { var_dump($v); }
