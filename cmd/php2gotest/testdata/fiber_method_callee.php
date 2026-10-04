<?php
// A suspending callee reached as a **method**. Which method runs is a run-time
// fact -- an interface-typed variable holds any implementor -- so the frame
// constructor is looked up from the receiver's class chain exactly as the
// dispatcher looks up the body.
//
// The frame names its class, as a trace always does: `Direct->work`, not
// `work`.

class Direct {
    public function work($v) { return Fiber::suspend($v) . "!"; }
}
class Sub extends Direct {}

$f = new Fiber(function () { $d = new Direct; return $d->work("a"); });
var_dump($f->start());
var_dump($f->resume("R"));
var_dump($f->getReturn());

// Inherited: the method is declared in the parent and reached on the child.
$g = new Fiber(function () { $d = new Sub; return $d->work("b"); });
var_dump($g->start());
var_dump($g->resume("S"));
var_dump($g->getReturn());

// An object with `__invoke` is a callable, so `new Fiber($obj)` enters the
// method's frame rather than refusing.
class Invocable {
    public function __invoke($v) {
        $got = Fiber::suspend($v);
        throw new Exception("from __invoke: $got");
    }
}
$h = new Fiber(new Invocable);
var_dump($h->start("c"));
try { $h->resume("T"); } catch (Throwable $e) {
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n--\n";
}
