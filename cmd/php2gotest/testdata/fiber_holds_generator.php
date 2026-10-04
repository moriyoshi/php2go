<?php
// A generator stopped on a Fiber::suspend() inside its body is *running* to
// the engine -- it is on the suspended fiber's stack -- until the fiber
// resumes it or is destroyed.

function report(Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

// 1. Parked after its first yield: reads answer the element it holds,
// resuming it from outside the fiber is refused.
function g1() {
    yield 'a' => 'foo';
    $x = Fiber::suspend(1);
    echo "g1 resumed with $x\n";
    yield 'b' => 'bar';
}
$it = g1();
$f = new Fiber(function () use ($it) {
    var_dump($it->current());
    $it->next();
    echo "fiber: after next\n";
    var_dump($it->current());
});
$f->start();
var_dump($it->current(), $it->key(), $it->valid());
try { $it->next(); } catch (Throwable $e) { report($e); }
try { $it->send(5); } catch (Throwable $e) { report($e); }
try { $it->throw(new Exception("x")); } catch (Throwable $e) { report($e); }
try { $it->rewind(); } catch (Throwable $e) { report($e); }
try { $it->getReturn(); } catch (Throwable $e) { report($e); }
try { foreach ($it as $v) { echo "never\n"; } } catch (Throwable $e) { report($e); }
$f->resume(7);
var_dump($it->current());

// 2. Parked before its first yield: it is still being primed, so even the
// reads resume it.
function g2() {
    $x = Fiber::suspend(2);
    echo "g2 resumed with $x\n";
    yield 'z';
}
$it2 = g2();
$f2 = new Fiber(function () use ($it2) { var_dump($it2->current()); });
$f2->start();
foreach (['current', 'key', 'valid', 'rewind', 'next'] as $m) {
    try { $it2->$m(); echo "$m answered\n"; } catch (Throwable $e) { echo "$m: "; report($e); }
}
$f2->resume(8);

// 3. A delegate that suspends before its first yield parks the generator
// delegating to it, and resuming the fiber continues the delegate.
function inner3() {
    try {
        echo "inner3 before suspend\n";
        $v = Fiber::suspend('i3');
        echo "inner3 got $v\n";
        yield 'from inner3';
        return 'inner3 done';
    } finally {
        echo "inner3 finally\n";
    }
}
function outer3() {
    try {
        $r = yield from inner3();
        echo "outer3 got $r\n";
        yield 'outer3 own';
    } finally {
        echo "outer3 finally\n";
    }
}
$g3 = outer3();
$f3 = new Fiber(function () use ($g3) {
    var_dump($g3->current());
    $g3->next();
    var_dump($g3->current());
    return 'f3 ret';
});
var_dump($f3->start());
$f3->resume('hello');
var_dump($f3->getReturn());
$g3->next();
var_dump($g3->valid());

// 4. What the delegate throws after the fiber resumes it continues into the
// delegating body at its `yield from`.
function inner4() {
    yield 1;
    Fiber::suspend();
    throw new RuntimeException("inner4 threw");
}
function outer4() {
    try {
        yield from inner4();
    } catch (RuntimeException $e) {
        echo "outer4 caught: ", $e->getMessage(), "\n";
        yield 'outer4 recovered';
    }
}
$g4 = outer4();
$f4 = new Fiber(function () use ($g4) {
    $g4->current();
    $g4->next();
    var_dump($g4->current());
});
$f4->start();
$f4->resume();

// 5. throw() and getReturn() on a generator, from a body that drives
// generators through a fiber-aware call, reach the generator.
function g5() {
    try {
        yield 1;
    } catch (LogicException $e) {
        echo "g5 caught: ", $e->getMessage(), "\n";
        yield 2;
    }
    return 'g5 ret';
}
$g5 = g5();
$g5->current();
var_dump($g5->throw(new LogicException("thrown in")));
$g5->next();
var_dump($g5->getReturn());
try { $g5->nosuch(); } catch (Throwable $e) { report($e); }

// 6. foreach over a generator already past its first yield is a rewind.
function g6() { yield 1; yield 2; }
$g6 = g6();
$g6->next();
try { foreach ($g6 as $v) { var_dump($v); } } catch (Throwable $e) { report($e); }

// 7. A fiber left suspended inside a delegation chain is unwound at the end:
// the innermost generator's finally first, then the one delegating to it,
// then the fiber's own.
function inner7() {
    try {
        yield 'i7';
        Fiber::suspend();
        echo "never\n";
    } finally {
        echo "inner7 finally\n";
    }
}
function outer7() {
    try {
        yield from inner7();
    } finally {
        echo "outer7 finally\n";
    }
}
$g7 = outer7();
$f7 = new Fiber(function () use ($g7) {
    try {
        $g7->current();
        $g7->next();
    } finally {
        echo "fiber7 finally\n";
    }
});
$f7->start();
echo "end of script\n";
