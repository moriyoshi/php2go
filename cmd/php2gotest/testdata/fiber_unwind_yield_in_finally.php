<?php
// A fiber destroyed while a generator it drives is stopped on its suspension
// unwinds into that generator: its finally blocks run. A `yield` or
// `yield from` reached there is an ordinary yield -- the driver call inside
// the fiber returns and the fiber's code carries on -- and the generators
// left suspended are destroyed later, as objects of their own, where a
// `yield from` in a finally is refused with an Error they can catch.

function inner() {
    try {
        yield 1;
        Fiber::suspend();
    } finally {
        echo "inner finally\n";
        yield 5;
        echo "never: inner after yield\n";
    }
}

function outer() {
    try {
        yield from inner();
    } finally {
        echo "outer finally\n";
        try {
            yield from [1, 2];
        } catch (Error $e) {
            echo "outer refused: ", $e->getMessage(), "\n";
        }
        echo "outer finally done\n";
    }
}

// A `yield from` in its finally yields the array's first element to the
// `next()` in the fiber. The fiber is destroyed here, mid-script.
function leaf() {
    try {
        yield 1;
        Fiber::suspend();
    } finally {
        echo "leaf finally\n";
        try {
            yield from [1, 2];
        } catch (Error $e) {
            echo "never: leaf refused\n";
        }
        echo "never: leaf finally done\n";
    }
}
$l = leaf();
$fl = new Fiber(function () use ($l) {
    try {
        $l->current();
        $l->next();
        echo "leaf fiber after next\n";
    } finally {
        echo "leaf fiber finally\n";
    }
});
$fl->start();
$fl = null;
echo "leaf fiber destroyed\n";

$g = outer();
$f = new Fiber(function () use ($g) {
    try {
        $g->current();
        $g->next();
    } finally {
        echo "fiber finally\n";
    }
});
$f->start();
echo "end\n";
