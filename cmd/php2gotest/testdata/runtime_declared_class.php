<?php
// A class declared at **run time** is linked when its DECLARE_CLASS runs, so
// the harvested entry is unlinked: nothing of the parent has been flattened
// into it. Writing an inherited property was then a *dynamic* property, which
// is deprecated where PHP is silent.

class Base {
    public $open = 'base-open';
    protected $shared = 'base-shared';
    private $hidden = 'base-hidden';
    public static $counter = 0;
    public function __construct() { $this->open = ++self::$counter; }
    public function sees() { return $this->hidden; }
}

class Eager extends Base {}

if (true) {
    class Lazy extends Base {}
}

$e = new Eager;
$l = new Lazy;
var_dump($e, $l);
var_dump($e->sees(), $l->sees());
var_dump(Base::$counter);

// Redeclaring a protected or public property keeps **one** slot; a private is
// one slot per declaring class.
class Sharer extends Base {
    public $open = 'sharer-open';
    protected $shared = 'sharer-shared';
    private $hidden = 'sharer-hidden';
}
var_dump(new Sharer);

// `++self::$prop` is a pre-increment on a static property: op1 is the name and
// op2 the class, the opposite of the object form.
class Counter {
    private static $n = 0;
    public static function step() { return ++self::$n; }
    public static function back() { return --self::$n; }
    public static function post() { return self::$n++; }
}
var_dump(Counter::step(), Counter::step(), Counter::post(), Counter::back());
