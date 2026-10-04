<?php
// Casting a fresh object to an array frees the object once its properties
// are copied out: the next object takes its number.
class Keeper {
    public static $all = [];
    public $k = 1;
    public function __construct() { self::$all[] = $this; }
}
// A constructor that keeps $this keeps the object alive.
var_dump((array) new Keeper);
var_dump(new stdClass);

class Pair {
    public $a = 1;
    protected $b = 2;
}
var_dump((array) new Pair);
var_dump(new stdClass);
$o = new stdClass;
$o->x = 1;
var_dump((array) $o, $o);
