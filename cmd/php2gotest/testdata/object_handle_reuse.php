<?php
// The engine hands the lowest free object handle to the next `new`, so a
// temporary that dies at the call consuming it gives its number straight back.
// Every case below is here to try to break that claim, not to confirm it.
class P { public $v = 1; }

// 1. Plain temporaries: each frees its handle, so both are #1.
var_dump(new P());
var_dump(new P());

// 2. A temporary kept in a local must NOT free its handle.
$keep = new P();
var_dump($keep);
var_dump(new P());
var_dump($keep);

// 3. Passed to a user function that stores it: the callee keeps it alive.
$store = null;
function grab($o) { global $store; $store = $o; }
grab(new P());
var_dump(new P());
var_dump($store);

// 4. An argument to somebody else's constructor, which holds it.
class Holder { public $in; public function __construct($i) { $this->in = $i; } }
$h = new Holder(new P());
var_dump($h);
var_dump(new P());

// 5. Returned from a function: it outlives the call.
function make() { return new P(); }
$r = make();
var_dump($r);
var_dump(new P());

// 6. A group dying together. The engine frees the array's elements in array
//    order and reuses the **most recently** freed, so the next object is #2
//    and not #1 -- the free list is a stack, which one object alone can never
//    show.
var_dump([new P(), new P()]);
var_dump(new P());
var_dump(new P());

// 7. A group where one element is kept: nothing in it may be recycled.
$held = new P();
var_dump([new P(), $held]);
var_dump(new P());
var_dump($held);
