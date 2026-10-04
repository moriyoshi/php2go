<?php
// var_dump and print_r of SPL container subclasses with non-public and
// uninitialized properties: the properties under their mangled names,
// then the container's own private entries.
class P1 extends SplObjectStorage { public $a = 1; protected $b = 2; private $c = 3; public int $u; }
class P2 extends SplStack { private $c = 3; protected $d = [1]; }
class P3 extends MultipleIterator { private $c = 3; }
class P4 extends SplMinHeap { private $c = 3; }
$p = new P1; $p[new stdClass] = 'i';
$q = new P2; $q->push(1); $q->dyn = 4;
var_dump($p, $q, new P3, new P4);
print_r($p); print_r($q);
