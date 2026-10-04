<?php
// `unset($$n)` names a variable at run time, in the local table or the global
// one depending on the scope bit -- the same shape `isset` and the fetch family
// have.
$x = 1;
$n = "x";
var_dump(isset($x));
unset($$n);
var_dump(isset($x), isset($$n));
$x = 2;
var_dump($x);

$g = 5;
function unsetsGlobal() { unset($GLOBALS["g"]); }
unsetsGlobal();
var_dump(isset($g));

// A static call whose method is named by a value.
class C {
    public static function m($a) { return "m:$a"; }
    public static function other() { return "other"; }
}
$name = "m";
var_dump(C::$name(1));
$cls = "C";
var_dump($cls::$name(2), $cls::m(3), $cls::other());

// `parent::__construct()` leaves the method **out of the opline** -- the
// compiler resolved it and left a cache address where the name would be -- so
// all four spellings arrive with an unused op2.
class Base {
    public $seen = [];
    public function __construct($tag = "base") { $this->seen[] = $tag; }
}
class Child extends Base {
    public function __construct() {
        parent::__construct("from-parent");
        Base::__construct("named-explicitly");
    }
}
$c = new Child;
var_dump($c->seen);

// A `local.get` whose operand is a **place**: the value of a bind, read back
// through the place the bind wrote rather than out of a slot.
$arr = [1, 2];
$b = "bound";
var_dump($arr[0] =& $b);
$b = "changed";
var_dump($arr);
$o = new stdClass;
$o->p = 1;
var_dump($o->p =& $b);
