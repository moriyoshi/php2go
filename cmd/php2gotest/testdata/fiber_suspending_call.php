<?php
// `Fiber::suspend()` suspends the whole stack it runs on, so a function that
// *calls* a suspending one has to be able to give control up as well. The CPS
// rewrite turns such a call into a suspension point: the caller builds the
// callee's frame and hands it up rather than performing the call, and resumes
// with the callee's **return value**.
//
// A value defined before the call and read after it moves into the frame. It
// has to: the caller returns and is re-entered, so a Go local is gone by then.
// `$a` below is the case, and losing it was silent -- the answer came back
// without it and nothing errored.

function inner($v) { return Fiber::suspend($v) . "!"; }

function outer($v) { $a = strlen($v); return $a . inner($v); }

$f = new Fiber('outer');
var_dump($f->start("ab"));
var_dump($f->resume("R"));
var_dump($f->getReturn());

// The same through a closure, which reaches the callee one level further out.
$g = new Fiber(function ($v) { $keep = strtoupper($v); return $keep . outer($v); });
var_dump($g->start("cd"));
var_dump($g->resume("S"));
var_dump($g->getReturn());

// Two suspending calls in one body, and one whose result is discarded.
function twice($v) { inner($v); $b = inner($v); return "<$b>"; }
$h = new Fiber('twice');
var_dump($h->start("x"));
var_dump($h->resume("1"));
var_dump($h->resume("2"));
var_dump($h->getReturn());

// A body that suspends but is never resumed still reports its status.
$i = new Fiber('outer');
$i->start("zz");
var_dump($i->isSuspended(), $i->isTerminated());
