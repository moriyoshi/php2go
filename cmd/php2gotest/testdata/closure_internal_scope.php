<?php
// A closure cannot take the scope of an engine class it did not already
// have: Closure::call() on a stdClass, or bindTo() naming one, warns and
// answers null. A user class, and binding an object with no scope, are fine.

$get = function () { return $this->x ?? "none"; };
$std = new stdClass;
$std->x = 7;
var_dump($get->call($std));
var_dump($get->bindTo($std, stdClass::class));
var_dump(Closure::bind($get, $std, 'ArrayObject'));
$unscoped = $get->bindTo($std);
var_dump($unscoped());
$unscoped = $get->bindTo($std, null);
var_dump($unscoped());

class Mine { private $x = "mine"; }
var_dump($get->call(new Mine));
var_dump($get->bindTo(new Mine, Mine::class)());
