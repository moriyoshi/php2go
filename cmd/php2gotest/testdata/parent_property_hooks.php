<?php
// `parent::$x::get()` inside a property hook reaches the **parent chain's**
// hook for that property. The opline carries the property name in op1 and the
// hook kind in an unused op2 -- 0 for get, 1 for set.

class Base {
    public mixed $x = "base" {
        get { echo "Base::get\n"; return $this->x; }
        set { echo "Base::set($value)\n"; $this->x = $value; }
    }
}
class Child extends Base {
    public mixed $x {
        get { echo "Child::get\n"; return parent::$x::get() . "+child"; }
        set { echo "Child::set($value)\n"; parent::$x::set($value . "!"); }
    }
}
$c = new Child;
$c->x = "v";
var_dump($c->x);

// A hook two levels up answers when the level between declares none.
class Middle extends Base {}
class Leaf extends Middle {
    public mixed $x {
        get { return "leaf(" . parent::$x::get() . ")"; }
        set { parent::$x::set("via-leaf:" . $value); }
    }
}
$l = new Leaf;
$l->x = "deep";
var_dump($l->x);

// Where the chain has no hook at all the property is a plain slot, and the call
// is the raw backing-store access.
class Plain { public mixed $y = "plain"; }
class Hooked extends Plain {
    // The default is restated: a hooked redeclaration drops the parent's, and
    // reading an uninitialised typed property is an Error PHP raises and this
    // compiler does not yet model.
    public mixed $y = "plain" {
        get { return "[" . parent::$y::get() . "]"; }
        set { parent::$y::set("UP:" . $value); }
    }
}
$h = new Hooked;
var_dump($h->y);
$h->y = "written";
var_dump($h->y);

// A one-sided child hook: the get is the child's and the set is inherited.
class OneSided extends Base {
    public mixed $x {
        get { return "only-get:" . parent::$x::get(); }
    }
}
$o = new OneSided;
$o->x = "through-base";
var_dump($o->x);
