<?php
// `use T;` is resolved at compile time by the engine, which copies the trait's
// members into the class. The IR keeps the link instead, and every table
// downstream walked the parent chain only -- so nothing reached a trait member
// and every call was `Call to undefined method`.
//
// The precedence rules are the point of most of these: the class's own member
// wins, and a trait's beats an inherited one.
trait Greets {
    public $greeting = "hello";
    public static $count = 0;
    public function greet() { return $this->greeting; }
    public static function bump() { self::$count = self::$count + 1; return self::$count; }
}

trait Shouts {
    public function shout() { return "HEY"; }
}

class Plain { use Greets; }
$p = new Plain();
var_dump($p->greet(), $p->greeting, Plain::bump(), Plain::$count);

// Two traits at once, and a class member that wins over the trait's.
class Both {
    use Greets, Shouts;
    public function greet() { return "own"; }
}
$b = new Both();
var_dump($b->greet(), $b->shout());

// A trait beats an inherited method.
class Parent1 { public function greet() { return "parent"; } }
class Child extends Parent1 { use Greets; }
$ch = new Child();
var_dump($ch->greet());

// A trait using a trait.
trait Outer { use Shouts; public function outer() { return "outer"; } }
class Nested { use Outer; }
$ne = new Nested();
var_dump($ne->shout(), $ne->outer());

// **Each using class gets its own copy of a trait's static.** Sharing one slot
// is the shape this had before the using class became the declarer.
class A1 { use Greets; }
class B1 { use Greets; }
A1::bump();
A1::bump();
B1::bump();
var_dump(A1::$count, B1::$count);

// Properties keep declaration order and visibility, which var_dump prints.
// Every object here is held in a variable rather than created as a temporary:
// PHP reuses the handle of a temporary it has freed, and php2go does not yet,
// so a throwaway `new` would make this file fail on handle numbering -- a gap
// that has nothing to do with traits.
trait Fields { public $pub = 1; protected $prot = 2; private $priv = 3; }
class Visible { use Fields; public $own = 4; }
$vis = new Visible();
var_dump($vis);

// `use T { m as n; }` composes the body under a **second name**. The class
// table is keyed by the name the class knows a method by, not by the function's
// own name -- keying on the latter emitted the same key twice, which does not
// compile, and is what made aliases look unimplementable.
//
// Which table gains the alias is the trait's to say: an instance method's alias
// is an instance method and a static one's is static. Adding it to both, on the
// guess that one would be right, emitted a duplicate key again.
trait Aliased {
    public function original() { return "instance"; }
    public static function statically() { return "static"; }
}

class Renamer {
    use Aliased {
        original as renamed;
        statically as renamedStatic;
    }
}

$r = new Renamer();
var_dump($r->original(), $r->renamed());
var_dump(Renamer::statically(), Renamer::renamedStatic());
