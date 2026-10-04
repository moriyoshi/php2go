<?php
// A plain object walked by reference binds to the **property slots**, so the
// array handed to the iterator holds the object's own boxes. A detached
// snapshot would swallow every write, which is a wrong answer that looks like a
// working loop.
class O { public $a = 1; public $b = 2; protected $p = 3; private $q = 4; }
$o = new O;
foreach ($o as $k => &$v) { $v = $v * 10; }
unset($v);
var_dump(get_object_vars($o));

// From outside the class only the public properties are walked, so `$p` and
// `$q` are untouched above. From inside, all of them are.
class Inner {
    public $x = 1;
    protected $y = 2;
    private $z = 3;
    function walk() { foreach ($this as $k => &$v) { $v++; } unset($v); }
    function seen(): array { return get_object_vars($this); }
}
$i = new Inner;
$i->walk();
var_dump($i->seen());

// The element still bound when the loop ends keeps its box; the ones the walk
// moved past collapse.
$j = new O;
foreach ($j as $k => &$w) { }
var_dump(get_object_vars($j));

// An uninitialised typed property has a slot and no value, and is not walked.
class Typed { public int $set = 1; public int $unset; }
$t = new Typed;
foreach ($t as $k => &$u) { $u = 9; }
unset($u);
var_dump(get_object_vars($t));

// A readonly property has no reference to give.
class Locked { public function __construct(public readonly int $n) {} }
$l = new Locked(1);
try { foreach ($l as &$r) { } } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A reference bound to a **typed** property is itself typed, and a loop
// variable is such a reference: the constraint travels with the box.
class Typing { public int $n = 1; }
$ty = new Typing;
foreach ($ty as &$m) { $m = "21"; }
var_dump(get_object_vars($ty));
try { $m = "x"; } catch (TypeError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
unset($m);
