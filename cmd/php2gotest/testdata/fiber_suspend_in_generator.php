<?php
// A `Fiber::suspend()` inside a generator's body has to travel out through the
// **driver call** -- `$g->next()` -- and back in when the fiber resumes. That is
// only possible if the frame making the driver call is itself a state machine,
// which is rule 2 in `internal/lower/ir.go` and the last form of the caller
// question.
//
// The generator is left **parked** while the fiber is away: stopped mid-step,
// not finished and not broken. Resuming the fiber resumes the body exactly
// where it stopped.
//
// A driver call splits whether or not the receiver's class was inferred, which
// is safe because a receiver with no frame of its own gets one that performs
// the ordinary call -- so an over-eager split costs a resumption and changes
// nothing.
function gen() {
    yield 1;
    Fiber::suspend("from gen");
    yield 2;
}
$f = new Fiber(function () {
    $g = gen();
    var_dump($g->current());
    $g->next();
    var_dump($g->current());
    return "done";
});
var_dump($f->start());
var_dump($f->resume("R"));
var_dump($f->getReturn());

// A driver call on something that is not a generator at all still works: the
// split is static and conservative, and the fallback performs the call.
class Counter {
    private $n = 0;
    public function next() { return ++$this->n; }
    public function current() { return $this->n; }
}
$c = new Counter;
var_dump($c->next(), $c->next(), $c->current());

// And a generator driven entirely outside a fiber is untouched by any of this.
function plain() { yield 1; yield 2; return 9; }
$p = plain();
foreach ($p as $v) { var_dump($v); }
var_dump($p->getReturn());
