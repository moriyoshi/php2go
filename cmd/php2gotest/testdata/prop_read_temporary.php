<?php
// A property read on a fresh object frees that object once the property is
// fetched: the next object takes its number.

// A constructor that keeps $this keeps the object alive -- here and through
// a method call on the fresh object, or an inherited constructor.
class Kept {
    public static $all = [];
    public $v = 1;
    public function __construct() { self::$all[] = $this; }
    public function get() { return $this->v; }
}
class KeptChild extends Kept {}
var_dump((new Kept)->v);
var_dump(new stdClass);
var_dump((new Kept)->get());
var_dump(new stdClass);
var_dump((new KeptChild)->v);
var_dump(new stdClass);
var_dump(count(Kept::$all));

class Plain {
    public $n = 7;
}
var_dump((new Plain)->n);
var_dump(new stdClass);

class Box {
    public $inner;
    public function __construct() { $this->inner = new stdClass; }
}
var_dump(clone (new Box)->inner);
