<?php
// **An undecided send: only the callee knows whether it wanted a reference.**
//
// `f($a["k"])` fetches the element as a write target and leaves the decision to
// the callee (the engine's ZEND_SEND_FUNC_ARG). Calling that mode "by value"
// read the element instead of binding it, so a dynamic call warned about an
// undefined key and left the array untouched where the engine autovivifies and
// binds. A *static* call was already right, because the signature is in hand.
function byref(&$x) { $x = 5; }
function byval($x)  { $x = 5; }

// Static: settled at compile time.
$s = [];
byref($s["k"]);
var_dump($s);
$t = [];
byval($t["k"]);
var_dump($t);

// Dynamic through a string: settled at run time, from the callee value.
$f = "byref";
$a = [];
$f($a["k"]);
var_dump($a);
$g = "byval";
$b = [];
$g($b["k"]);
var_dump($b);

// A property target, and a nested element.
$o = new stdClass();
$f($o->prop);
var_dump($o);
// (A *nested* target -- `$f($n["outer"]["inner"])` -- is a separate,
// pre-existing gap: the static spelling fails identically, so it is not about
// the send mode. Filed in TODO.md.)

// Through a closure, which carries its own parameter list.
$c = function (&$y) { $y = 7; };
$d = [];
$c($d["k"]);
var_dump($d);
$e = function ($y) { $y = 7; };
$h = [];
$e($h["k"]);
var_dump($h);

// Two dynamic calls in one body must not collide in the emitted code.
$p = [];
$f($p["one"]);
$f($p["two"]);
var_dump($p);
echo "done\n";
