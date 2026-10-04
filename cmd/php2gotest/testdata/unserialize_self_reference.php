<?php
// unserialize() makes a stdClass in a program that never names the class,
// resolves `r:` to an object from inside its own properties, and fails the
// whole value -- at the offset just past it -- when `r:` names an array that
// is still being read.

$o = unserialize('O:8:"stdClass":2:{s:1:"x";r:1;s:1:"n";i:5;}');
var_dump(get_class($o), $o->x === $o, $o->n);
var_dump(unserialize('a:1:{i:0;r:1;}'));
var_dump(unserialize('a:2:{i:0;O:8:"stdClass":0:{}i:1;r:2;}')[1] instanceof stdClass);
class K { public $self; }
$k = unserialize('O:1:"K":1:{s:4:"self";r:1;}');
var_dump($k->self === $k);
