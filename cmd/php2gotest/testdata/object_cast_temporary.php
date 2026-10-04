<?php
// Casting an array to an object makes a fresh stdClass, which a call that
// only reads it leaves dead -- the next object takes its handle -- while
// casting an object gives back that same object (object_array_cast).

$arr = [1, 2];
var_dump((object) (array) $arr);
var_dump((object) ["a" => 1]);
$o = new stdClass;
var_dump((object) $o, (object) $o === $o);
var_dump(new stdClass);
