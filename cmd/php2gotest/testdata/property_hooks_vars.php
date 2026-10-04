<?php
// A hooked property is something to **run**, not a slot to read, and a virtual
// one has no slot at all. So `get_object_vars` and `var_export` walk the class's
// declaration order rather than the property table -- and the two differ, because
// the harvested table is sorted by slot offset and a virtual property has none.
class Base {
    public $a = 'a';
    public $virtual { get { return 'V'; } }
    public $backed = 'b' { get { return strtoupper($this->backed); } set { $this->backed = $value; } }
    protected $prot = 'prot';
    public $writeOnly { set { } }
}
class Derived extends Base {
    public $own = 'own' { get { return strtoupper($this->own); } }
    public $plain = 'plain';
}

$d = new Derived;
var_dump(get_object_vars($d));
var_export($d);
echo "\n";
echo json_encode($d), "\n";

// The raw table is untouched by any of that: a cast and var_dump both show the
// backing store, and neither lists a virtual property.
var_dump((array) $d);
var_dump($d);

// From inside, the protected property is reachable and the hooks still run.
class Reader extends Base {
    public function seen(): array { return get_object_vars($this); }
}
var_dump((new Reader)->seen());

// `protected(set)` is measured against the **first** class to declare the name,
// not the subclass that redeclared it, while the refusal names the
// redeclaration. The two disagree and both are observable.
class AsymBase { public protected(set) int $p = 1; }
class AsymMid extends AsymBase { public protected(set) int $p = 2; }
class AsymSib extends AsymBase { static function bump($o) { return $o->p += 1; } }
var_dump(AsymSib::bump(new AsymMid));
class AsymOutside { static function bump($o) { $o->p = 9; } }
try { AsymOutside::bump(new AsymMid); } catch (Error $e) { echo $e->getMessage(), "\n"; }
