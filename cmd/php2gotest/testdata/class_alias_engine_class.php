<?php
// An engine class can be aliased like a declared one: the alias constructs,
// tests and catches as the class itself, which keeps its own name
// (class_alias_006).

var_dump(class_alias('stdclass', 'Plain'));
$p = new Plain;
$p->x = 1;
var_dump($p, get_class($p), $p instanceof Plain, $p instanceof stdClass);
var_dump(class_exists('plain'));

class_alias('ArrayObject', 'Bag');
$b = new Bag([1, 2]);
var_dump(count($b), get_class($b), $b instanceof Bag);

class_alias('RuntimeException', 'Oops');
try {
    throw new Oops("boom");
} catch (Oops $e) {
    var_dump(get_class($e), $e->getMessage());
}
var_dump(class_alias('NoSuchClass', 'Nope'));
