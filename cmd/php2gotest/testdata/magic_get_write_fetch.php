<?php
// Reaching through an overloaded property to write into it -- `$o->p->q =
// v`, `unset($o->p->q)`, `$o->p->q++` -- reads the container with `__get`
// alone; only an isset fetch (`isset()`, `??`) asks `__isset` first
// (bug71359).

class Bag {
    private $data = [];
    function __isset($n) { echo "__isset($n)\n"; return isset($this->data[$n]); }
    function &__get($n) {
        echo "__get($n)\n";
        if (!isset($this->data[$n])) {
            $this->data[$n] = new stdClass;
        }
        return $this->data[$n];
    }
    function __set($n, $v) { echo "__set($n)\n"; $this->data[$n] = $v; }
}

$b = new Bag;
$b->inner->q = 1;
var_dump($b->inner->q);
$b->inner->q++;
unset($b->inner->q);
var_dump(isset($b->inner->q));
var_dump($b->other->q ?? "d");
var_dump(isset($b->missing));
$b->fresh->x = 5;
var_dump($b->fresh);
