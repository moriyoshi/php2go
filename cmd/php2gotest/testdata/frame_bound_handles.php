<?php
// A promoted local is NOT a temporary, and the difference is a handle number.
//
// PHP frees a statement temporary at the end of its statement and a local when
// the frame dies. After slot promotion the two have the same shape -- a value
// with one surviving use -- so releasing at the last use is right for the first
// and early for the second. It is early by exactly the objects allocated in
// between, which is why this fixture always allocates something after the use.
class P { public $tag; function __construct($t = '') { $this->tag = $t; } }

// A true temporary: freed at the call, so the next object takes its handle.
function temporaries() {
    var_dump(spl_object_id(new P('t1')));
    var_dump(spl_object_id(new P('t2')));   // same number as t1
}
temporaries();

// A local: lives to frame end, so an object made after its last use gets a
// FRESH handle, not the local's.
function locals() {
    $a = new P('a');
    var_dump(spl_object_id($a));
    $b = new P('b');                        // must NOT reuse $a's handle
    var_dump(spl_object_id($b));
}
locals();

// A clone is a fresh object with no constructor; bound to a local it is
// frame-scoped like any other, and its handle returns when the frame ends.
function clones(P $src) {
    $c = clone $src;
    var_dump(spl_object_id($c));
    $d = clone $src;
    var_dump(spl_object_id($d));
}
$src = new P('src');
clones($src);
clones($src);

// A reflector receiver keeps no reference to itself, so its handle comes back
// at the end of the frame that made it.
function reflects(object $o) {
    $r = new ReflectionClass($o::class);
    var_dump($r->getShortName(), $r->hasProperty('tag'));
}
reflects($src);
reflects($src);
var_dump(spl_object_id(new P('last')));
var_dump($src->tag);
