<?php
// What a call does to its argument, by how the callee is named. A **known**
// callee has its send mode settled at compile time; a dynamically dispatched
// one does not, and the engine sends `ZEND_SEND_VAR_EX` so the callee's arginfo
// decides at run time.
//
// The by-reference half of that is an open gap -- see TODO, "Send a
// by-reference argument to a dynamically dispatched callee" -- so what is
// pinned here is the settled case and the by-value side of the undecided one,
// which must keep copying whichever way the call is made.

function appendBang(&$to) { $to .= "!"; }
function readOnly($from) { $from .= "?"; return $from; }

$known = "a";
appendBang($known);
var_dump($known);

// A by-value parameter reached through a variable must not alias the caller.
$byValue = "d";
$reader = "readOnly";
var_dump($reader($byValue));
var_dump($byValue);

$closureByValue = function ($x) { $x .= "?"; return $x; };
$untouched = "e";
var_dump($closureByValue($untouched));
var_dump($untouched);

// An array by value is copied, not shared, whichever way the call is made.
function pushCopy(array $a) { $a[] = "added"; return count($a); }
$list = [1, 2];
var_dump(pushCopy($list), count($list));
$pusher = "pushCopy";
var_dump($pusher($list), count($list));

// And by reference, with the callee known, it is not.
function pushShared(array &$a) { $a[] = "added"; }
$shared = [1, 2];
pushShared($shared);
var_dump($shared);

// A by-value object parameter still shares the instance: the handle is copied,
// not the object.
class Counter { public int $n = 0; }
function bump(Counter $c) { $c->n++; }
$counter = new Counter;
$bumper = "bump";
$bumper($counter);
bump($counter);
var_dump($counter->n);

// Through a variable holding the function name, and through a closure: the
// engine sends `ZEND_SEND_VAR_EX` and the **callee** decides.
$name = "appendBang";
$viaName = "b";
$name($viaName);
var_dump($viaName);

$closure = function (&$x) { $x .= "!"; };
$viaClosure = "c";
$closure($viaClosure);
var_dump($viaClosure);

$sharedPusher = "pushShared";
$sharedList = [1, 2];
$sharedPusher($sharedList);
var_dump($sharedList);

// The engine's own classes read their arguments directly rather than through a
// compiled parameter list, so they are receiving ends too.
$message = "from a variable";
$thrown = new Exception($message);
var_dump($thrown->getMessage());

function gen() { yield 1; }
$g = gen();
$toSend = "sent";
var_dump($g->current());
var_dump($g->send($toSend));

$bindee = function () { return "bound"; };
$holder = new stdClass;
$rebound = Closure::bind($bindee, $holder);
var_dump($rebound());

// A builtin whose parameter is by value must not see the box.
$forCount = [1, 2, 3];
var_dump(count($forCount));
$forClass = new stdClass;
var_dump(get_class($forClass));
$pieces = ["x", "y"];
$glue = "-";
var_dump(implode($glue, $pieces));
