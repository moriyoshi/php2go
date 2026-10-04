<?php
// `[&$u, &$v] = $src` destructures **by reference**: each element is fetched
// for write through a `make_ref` of the source, so the container reaches the
// dimension intrinsic as a value whose box is the storage to open.

$src = [10, 20];
[&$u, &$v] = $src;
$u = 99;
var_dump($src, $u, $v);

// Writing through the other name reaches the same slot.
$v = "changed";
var_dump($src);

// Mixed: some elements by reference and some by value.
$mixed = [1, 2, 3];
[$byVal, &$byRef, $alsoVal] = $mixed;
$byRef = "ref";
$byVal = "val";
var_dump($mixed, $byVal, $alsoVal);

// Keyed destructuring by reference.
$keyed = ["a" => 1, "b" => 2];
["a" => &$ka, "b" => $kb] = $keyed;
$ka = "written";
$kb = "not written back";
var_dump($keyed);

// A nested list with a reference at the inner level is left out on purpose:
// the outer element is fetched for write, which boxes it, and PHP drops that
// box again on the `ZEND_FREE` that ends the assignment. This compiler treats
// FREE as a no-op, so `var_dump` shows an `&` the engine does not. See TODO.md.

// The plain read form still works alongside it.
[$p, [$q, $r]] = [1, [2, 3]];
var_dump($p, $q, $r);
