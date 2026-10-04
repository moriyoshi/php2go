<?php
// Reaching a property as **storage** creates it, exactly where the `&` is: the
// engine puts NULL in the slot there and then, not the undef a fresh box would
// carry -- an undef slot is the uninitialised state of a *declared typed*
// property, which a dynamic one never is.
class Plain { public $x = 1; }

$o = new Plain;
$b = &$o->missing;
unset($b);
var_dump(get_object_vars($o));

// While a reference is held the element renders with its marker; the slot
// survives the holder either way.
$p = new Plain;
$r = &$p->later;
$r = 5;
var_dump(get_object_vars($p));

// Writing through an element place reaches the same creation.
$q = new Plain;
$q->list[] = "first";
var_dump(get_object_vars($q));

// A class that allows them creates silently.
#[AllowDynamicProperties]
class Allowed { public $x = 1; }
$a = new Allowed;
$s = &$a->free;
unset($s);
var_dump(get_object_vars($a));

// A readonly class refuses outright rather than deprecating, and the refusal is
// an Error the program can catch.
readonly class Locked { public function __construct(public int $n) {} }
$l = new Locked(1);
try { $t = &$l->nope; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
