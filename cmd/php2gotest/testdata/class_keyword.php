<?php
// `self`, `parent` and `static` arrive at FETCH_CLASS_NAME with **no operand**:
// the class is a fetch *type* in op1's value, and the low bits are the type
// while the rest are flags. `FETCH_CLASS` carries `512|type`; this opcode
// carries the bare type, so matching only the flagged spellings made
// `static::class` an unrecognised class reference and late static binding did
// not compile at all.
class Base {
    public static $tag = "Base";
    public static function whoStatic() { return static::class; }
    public static function whoSelf() { return self::class; }
    public static function tag() { return static::$tag; }
    public function whoThis() { return static::class; }
}

class Derived extends Base {
    public static $tag = "Derived";
    public function whoParent() { return parent::class; }
}

echo Base::whoStatic(), "|", Derived::whoStatic(), "\n";
echo Base::whoSelf(), "|", Derived::whoSelf(), "\n";
echo Base::tag(), "|", Derived::tag(), "\n";
$d = new Derived();
echo $d->whoThis(), "|", $d->whoParent(), "\n";
echo $d::class, "|", Derived::class, "\n";

// In a trait, `self::class` is the **using** class, which is what compiling one
// copy per using class buys.
trait Names { public function name() { return self::class; } }
class UserA { use Names; }
class UserB { use Names; }
$a = new UserA();
$b = new UserB();
echo $a->name(), "|", $b->name(), "\n";
