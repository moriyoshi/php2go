<?php
// A closure made from a function has no scope for good: call() and any
// bind that gives it a class scope warn "Cannot rebind scope of closure
// created from function" and answer null; binding only an object keeps
// the null scope (zend_valid_closure_binding, probed).
class a { function m($x){ return "m$x"; } static function s(){ return "s"; } }
function f($x = 1) { return "f$x"; }
$c = substr(...);
var_dump($c->call(new a, "abc", 1));
$d = f(...);
var_dump($d->call(new a, 5));
var_dump(Closure::bind($c, new a, a::class));
var_dump(Closure::bind($c, null, a::class));
var_dump(Closure::bind($c, new a) === null);
var_dump($c->bindTo(null, null) === null);
var_dump(Closure::bind($d, new a, a::class));
$e = (new a)->m(...);
var_dump($e->call(new a, 7));
$g = Closure::fromCallable('strlen');
var_dump($g->call(new a, "zz"));
var_dump($c->bindTo(null, 'a'));
var_dump($c("hello", 1, 2));
