<?php
// `$x = new D(...)` destroys the object `$x` held -- at the reassignment, not
// at script end -- and its handle is free for the next object.
//
// The engine's order, measured: the NEW object's constructor runs first, then
// the old one's destructor. So `ctor second` precedes `dtor first`, and the
// second object does NOT reuse the first's handle (the first is still alive
// while it is being constructed).
class D {
    public $t;
    function __construct($t) { $this->t = $t; echo "ctor $t\n"; }
    function __destruct() { echo "dtor {$this->t}\n"; }
}

$x = new D('first');
echo "--\n";
$x = new D('second');       // dtor first, AFTER ctor second
echo "--\n";
$x = null;                  // dtor second
echo "--\n";
echo "reused handle: ";
$y = new D('third');
echo spl_object_id($y), "\n";

// A local passed to a function that keeps nothing is still solely owned.
function looks(object $o) { var_dump($o->t); }
$p = new D('p1');
looks($p);
$p = new D('p2');           // dtor p1
echo "--\n";

// **The safety case.** A local whose object is kept elsewhere must NOT be
// released when the local is written again -- freeing a live handle prints as
// some other object, which is a wrong answer rather than a wrong number.
//
// The analysis is per *local*, not per value: because `$q` held one object that
// escaped, it claims no release for `$q` at all, and the objects below are
// destroyed at script end instead. That is later than the engine for `q2` but
// never earlier than is safe, so it is left that way deliberately -- see
// TODO.md. Script-end order for globals is reverse, which is why `q2` precedes
// `q1` at the bottom.
$keep = [];
$q = new D('q1');
$keep[] = $q;
$q = new D('q2');           // q1 is in $keep: NO dtor here
echo "kept: ", $keep[0]->t, "\n";
echo "end\n";
