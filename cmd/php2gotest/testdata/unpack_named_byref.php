<?php
// Unpacking a string key binds by name, and a by-reference parameter of
// that name takes the unpacked variable's own element -- a copy of the
// array made before is untouched. From a Traversable there is no element
// to bind, so it warns, naming the parameter's position, and passes by value.

function bump($a = null, &$b = null, $c = 0) { $b++; $c++; }

$ary = ['b' => 1, 'c' => 10];
$copy = $ary;
bump(...$ary);
var_dump($ary, $copy);

$pos = [5, 7];
bump(...$pos);
var_dump($pos);

$it = new ArrayIterator(['b' => 1]);
bump(...$it);
var_dump($it->getArrayCopy());
