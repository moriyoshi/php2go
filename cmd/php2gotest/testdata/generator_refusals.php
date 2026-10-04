<?php
// The refusals a Generator makes, and the classes they carry. Three of these
// are Error and two are Exception, and the split is not guessable -- each pair
// below came from the engine. `yield from` the running generator is the one
// that was not a wrong answer but a stack overflow.
function g() { yield 1; yield 2; }
$a = g();
echo "clone: "; try { $b = clone $a; echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "dynprop: "; try { $a->prop = 1; echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "throw non-exc: "; try { $a->throw(1); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$c = g();
foreach ($c as $v) {}
echo "rewind closed: "; try { $c->rewind(); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "traverse closed: "; try { foreach ($c as $v) {} echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$d = null;
function selfrun() { global $d; yield 1; $d->next(); }
$d = selfrun();
echo "resume running: "; try { foreach ($d as $v) {} echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$e2 = null;
function yfself() { global $e2; yield 1; yield from $e2; }
$e2 = yfself();
echo "yf running: "; try { foreach ($e2 as $v) {} echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "rewind after advance: ";
$f = g(); $f->next();
try { $f->rewind(); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// Cloning and dynamic properties are two different engine-class tables, not one.
$things = ['Generator' => g(), 'Closure' => function () {}, 'stdClass' => new stdClass, 'Exception' => new Exception("x")];
foreach ($things as $n => $o) {
    echo $n, " clone: ";
    try { $c = clone $o; echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
    echo $n, " dynprop: ";
    try { $o->zz = 1; echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}

// Every Throwable refuses to clone, subclasses included, and the message names
// the instance's own class.
class MyEx extends Exception {}
echo "subclass clone: ";
try { $c = clone new MyEx("x"); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// getReturn() **primes**: a body that returns before it ever yields has
// returned by the time it first suspends, which is never.
function early() { return 42; yield 24; }
var_dump(early()->getReturn());
function earlyNull(): Generator { return; yield 24; }
var_dump(earlyNull()->getReturn());
function late() { yield 24; return 7; }
$l = late();
echo "unfinished: ";
try { var_dump($l->getReturn()); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
