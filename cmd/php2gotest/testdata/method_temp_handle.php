<?php
// A temporary consumed by a **method call** dies when the call returns, and the
// engine hands its number straight back -- unless the method handed `$this`
// somewhere, in which case the object outlives the call. `LeaksThis` is what
// separates those, so every case below is one the analysis has to get right in
// a different direction.
//
// One case is deliberately absent: a method whose only use of `$this` is a
// closure that captures it. The engine frees the receiver there -- the closure
// is itself a temporary that dies at once -- while `LeaksThis` refuses every
// closure, so we keep the handle. A *missed* claim, which is the safe
// direction, and asserting the engine's answer here would pin behaviour we
// deliberately do not have.
class P { public $t = 1; }
class M {
    public $v = 1;
    public function read() { return 7; }              // keeps nothing
    public function touch() { $this->v = 2; }         // own property only
    public function keep() { global $held; $held = $this; }
    public function give() { return $this; }
    public function hand() { stash($this); }
    public function stow() { global $box2; $box2 = [$this]; }
}
$held = null;
$box = null;
$box2 = null;
function stash($o) { global $box; $box = $o; }

// Freed: nothing kept the receiver.
(new M)->read();
var_dump(spl_object_id(new P()));
(new M)->touch();
var_dump(spl_object_id(new P()));

// Not freed: each of these lets the receiver out.
(new M)->keep();
var_dump(spl_object_id(new P()));
$kept = (new M)->give();
var_dump(spl_object_id(new P()));
(new M)->hand();
var_dump(spl_object_id(new P()));
(new M)->stow();
var_dump(spl_object_id(new P()));

// Held in a local, so not a temporary at all.
$m = new M();
$m->read();
var_dump(spl_object_id(new P()));
var_dump($m->v, $held->v, $box->v, $kept->v, $box2[0]->v);

// An **engine** class has no compiled body to summarise, so its methods are
// answered from a measured list instead. `Exception::getMessage` keeps nothing,
// and the engine hands the exception's handle straight back.
(new Exception("x"))->getMessage();
var_dump(spl_object_id(new P()));
(new Exception("y"))->getTrace();
var_dump(spl_object_id(new P()));
