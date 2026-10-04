<?php
// get_object_vars answers what the *calling scope* can reach, which is why the
// compiled program answers it rather than the library table: from outside a
// class the public properties only, from inside also its own private and any
// protected in its hierarchy.
class B {
    public $a = 1;
    protected $b = 2;
    private $c = 3;
    public function insideB() { return get_object_vars($this); }
}
class C extends B {
    public $d = 4;
    private $e = 5;
    public function insideC() { return get_object_vars($this); }
}
class Unrelated {
    public function peek($o) { return get_object_vars($o); }
}

$o = new C;
var_dump(get_object_vars($o));
var_dump($o->insideB());
var_dump($o->insideC());
var_dump((new Unrelated)->peek($o));

// An inherited private is not there at all, from anywhere.
class P { private $p = 1; }
class Q extends P {}
var_dump(get_object_vars(new Q));

// An uninitialised typed property has a slot and no value, and is omitted.
class T { public int $set = 1; public int $never; public ?string $n = null; }
var_dump(get_object_vars(new T));

// A dynamic property is public and keeps whatever name it was given.
$s = new stdClass;
$s->x = 1;
$s->{"a b"} = 2;
var_dump(get_object_vars($s));
var_dump(get_object_vars(new stdClass));

// Not an object at all.
var_dump(get_object_vars($s) === (array) $s);

echo "--- get_class_vars ---\n";
class GB {
    public $ga = 1;
    protected $gb = 2;
    private $gc = 3;
    public static $gs = "S";
    public int $gu;
    public static function inside() { return get_class_vars("GB"); }
}
class GC extends GB {
    public $gd = 4;
    private $ge = 5;
    public static $gt = "T";
    public static function insideC() { return get_class_vars("GC"); }
}
var_dump(get_class_vars("GC"));
var_dump(GB::inside());
var_dump(GC::insideC());
GB::$gs = "changed";
var_dump(get_class_vars("GB"));
try {
    get_class_vars("no_such_class_at_all");
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
