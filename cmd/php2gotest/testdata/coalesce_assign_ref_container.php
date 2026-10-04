<?php
// `??=` on an element of what a by-reference function returned writes into
// that storage -- the container is a reference, and stays one -- and on a
// property of a method's result asks __isset before anything else.

function &store() { global $store; return $store; }
store()['bar'] ??= 42;
store()['bar'] ??= 43;
store()['baz'] = 1;
var_dump($store);

class Magic
{
    public $prop;
    public function self() { return $this; }
    public function __isset($n) { echo "__isset($n)\n"; return false; }
    public function __get($n) { echo "__get($n)\n"; return "got"; }
    public function __set($n, $v) { echo "__set($n)\n"; }
}
$m = new Magic;
$m->self()->missing ??= 1;
var_dump($m->self()->other ?? "default");
