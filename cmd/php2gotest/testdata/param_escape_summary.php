<?php
// Whether a *compiled* callee keeps a reference to its argument decides when
// the caller may hand the object's handle back. `nonRetainingCallee` names
// library functions by hand; this is the same fact derived from the body.
//
// Written as an attack on the claim: every function below that DOES retain must
// keep its argument alive, and getting that wrong reuses a live handle -- which
// prints as some other object, not as a wrong number.
class P { public $tag; function __construct($t) { $this->tag = $t; } }

$keep = [];
$slot = null;

function touches($o) { $o->tag = "touched"; var_dump($o->tag); }   // keeps nothing
function reads($o)   { return $o->tag; }                          // keeps nothing
function stashes($o) { global $keep; $keep[] = $o; }              // RETAINS
function assigns($o) { global $slot; $slot = $o; }                // RETAINS
function chains($o)  { touches($o); }                             // keeps nothing, via a callee
function leaks($o)   { stashes($o); }                             // RETAINS, via a callee

// A handle freed at the call is reused by the next object.
touches(new P("a"));
echo spl_object_id(new P("b")), "\n";
var_dump(reads(new P("c")));
echo spl_object_id(new P("d")), "\n";
chains(new P("e"));
echo spl_object_id(new P("f")), "\n";

// A retained object must NOT have its handle reused.
stashes(new P("kept1"));
$after = new P("after1");
echo spl_object_id($after), "\n";
var_dump($keep[0]->tag);

assigns(new P("kept2"));
$after2 = new P("after2");
echo spl_object_id($after2), "\n";
var_dump($slot->tag);

leaks(new P("kept3"));
$after3 = new P("after3");
echo spl_object_id($after3), "\n";
var_dump($keep[1]->tag);

// Recursion stays unclaimed rather than justifying itself in a circle.
function recurses($o, $n) { if ($n > 0) { recurses($o, $n - 1); } return $n; }
recurses(new P("rec"), 2);
echo spl_object_id(new P("post-rec")), "\n";

// A by-reference parameter aliases the caller's storage.
function byref(&$o) { $o = new P("replaced"); }
$r = new P("orig");
byref($r);
var_dump($r->tag);
var_dump(count($keep));
