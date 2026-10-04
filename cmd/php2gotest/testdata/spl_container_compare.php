<?php
// The containers' own compare handlers: SplObjectStorage compares only
// its storage and only with SplObjectStorage itself; ArrayObject and
// ArrayIterator compare their storage before their properties.
class Props extends SplObjectStorage { public $pub = 1; }
$o = new stdClass;
$a = new SplObjectStorage; $b = new SplObjectStorage;
var_dump($a == $b, new Props == new Props, new Props <=> new Props, new Props < new Props);
$a[$o] = 1; var_dump($a == $b, $a > $b); $b[$o] = 1; var_dump($a == $b); $b[$o] = 2; var_dump($a == $b, $a < $b);
$a->x = 5; var_dump($a == $b);
var_dump(new ArrayObject([1]) == new ArrayObject([2]), new ArrayObject([1]) == new ArrayObject([1]), new ArrayObject([1]) < new ArrayObject([2]), new ArrayIterator([1]) == new ArrayObject([1]));
$p = new ArrayObject([1]); $p->z = 1; var_dump($p == new ArrayObject([1]));
