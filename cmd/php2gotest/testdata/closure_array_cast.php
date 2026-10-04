<?php
// Casting a Closure to an array wraps it rather than listing its properties.
$f = function () { return "called"; };
$a = (array) $f;
var_dump(count($a), $a[0] === $f, $a[0]());

$g = static fn() => 3;
var_dump(count((array) $g));

// array_splice takes a non-array replacement through the same cast.
$items = [1, 2, 3];
array_splice($items, 0, 1, $f);
var_dump(count($items), $items[0] === $f);

// Other objects still hand over their properties.
$o = new stdClass;
$o->p = 1;
var_dump((array) $o);
