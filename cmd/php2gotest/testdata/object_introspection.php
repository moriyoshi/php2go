<?php
// What an object shows, and to whom. The visible set depends on the **calling
// scope**, so `var_dump` and `foreach` from outside, from the child and from
// the base all give different answers -- and `property_exists` gives a fourth,
// because a parent's private property is not part of the child even though the
// instance carries the slot.

class Base {
    public $pub = 1;
    protected $prot = 2;
    private $priv = 3;
    public ?int $typed = null;
    public int $uninit;
    public static $stat = "s";
    public function dumpInside() { var_dump($this); foreach ($this as $k => $v) { echo "  in:$k\n"; } }
}
class Child extends Base {
    private $childPriv = 4;
    public $extra = 5;
    public function dumpChild() { var_dump($this); foreach ($this as $k => $v) { echo "  ch:$k\n"; } }
}
$c = new Child;
echo "--- from outside\n";
var_dump($c);
foreach ($c as $k => $v) { echo "  out:$k\n"; }
echo "--- from inside the child\n";
$c->dumpChild();
echo "--- from inside the base\n";
$c->dumpInside();
echo "--- introspection\n";
var_dump(property_exists($c, "priv"), property_exists($c, "childPriv"), property_exists($c, "nope"));
var_dump(method_exists($c, "dumpInside"), method_exists("Child", "dumpChild"));
var_dump(get_class($c), get_parent_class($c));
echo "--- dynamic property\n";
class Open {}
$o = new Open;
@$o->added = 1;
var_dump($o);
foreach ($o as $k => $v) { echo "  dyn:$k\n"; }
echo "--- object with a NUL-mangled private in an array cast\n";
var_dump((array)$c);

// A parent's private is not part of the child, whichever way it is asked.
class Owner { private $secret = 1; protected $shared = 2; public $open = 3; public int $blank; }
class Heir extends Owner { private $mine = 4; }
$heir = new Heir;
var_dump(property_exists($heir, "secret"), property_exists("Heir", "secret"), property_exists("Owner", "secret"));
var_dump(property_exists($heir, "shared"), property_exists($heir, "mine"), property_exists($heir, "blank"));
var_dump(property_exists($heir, "absent"));

// A dynamic property is on the instance and in no class table, so the object
// answers for exactly the names the table does not know.
@$heir->attached = 5;
var_dump(property_exists($heir, "attached"));

// An **uninitialised** typed property has a slot and no value: the array cast
// copies values, so it is not an element. `var_dump` shows it as
// `uninitialized`, which is a different question.
var_dump((array) $heir);
var_dump($heir);

// `property_exists` is **scope-sensitive**: a parent's private property is
// reported when the caller is that parent, and not when it is anyone else.
class Declarer {
    private $hidden = 1;
    public function fromInstanceMethod($o) { return property_exists($o, 'hidden'); }
    public static function fromStaticMethod($o) { return property_exists($o, 'hidden'); }
}
class Inheritor extends Declarer {
    public function fromChild($o) { return property_exists($o, 'hidden'); }
}
$sub = new Inheritor;
var_dump($sub->fromInstanceMethod($sub));
var_dump(Declarer::fromStaticMethod($sub));
var_dump($sub->fromChild($sub));
var_dump(property_exists($sub, 'hidden'));
