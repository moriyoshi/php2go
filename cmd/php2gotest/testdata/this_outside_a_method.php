<?php
// Reading `$this` where there is no object is a **catchable Error**, not
// something a compiler may decline: the program runs and carries on if it
// catches. `isset($this)` is a different opcode and answers false in silence,
// which is why the two do not share an answer.

function reads() { return $this; }
try { reads(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

function writes() { $this->a = 1; }
try { writes(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

function calls() { return $this->m(); }
try { calls(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

function asks() { var_dump(isset($this)); }
asks();

function emptily() { var_dump(empty($this)); }
emptily();

// Inside a method it is the receiver, and a closure written there keeps it.
class Holder {
    public $v = "held";
    public function read(): string { return $this->v; }
    public function viaClosure(): string { $c = function () { return $this->v; }; return $c(); }
    public function viaArrow(): string { $a = fn() => $this->v; return $a(); }
    public function asks(): bool { return isset($this); }
}
$h = new Holder;
var_dump($h->read(), $h->viaClosure(), $h->viaArrow(), $h->asks());

// A static method has no `$this` either.
class Statically {
    public static function reads() { return $this; }
    public static function asks() { return isset($this); }
}
try { Statically::reads(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump(Statically::asks());
