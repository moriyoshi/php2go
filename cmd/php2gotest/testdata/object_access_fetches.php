<?php
// A private(set) property holding an object may be reached with & from
// outside its scope: the reference is to a copy of the handle, as for a
// readonly one. Holding anything else, it is refused.
class Bar { public $x = 0; }
class Foo {
    public private(set) Bar $bar;
    public private(set) int $i = 1;
    public private(set) ?Bar $n = null;
    public protected(set) mixed $m;
    public function __construct() { $this->bar = new Bar(); $this->m = new Bar(); }
    public function inside() { $r = &$this->bar; $r = new Bar; $r->x = 4; var_dump($this->bar->x); }
}
function t(&$z) { $z = 9; }
function test() {
    $foo = new Foo();
    $r = &$foo->bar; var_dump($r === $foo->bar);
    $r = 5; var_dump($foo->bar);
    try { $q = &$foo->i; } catch (Error $e) { echo $e->getMessage(), "\n"; }
    try { $q = &$foo->n; } catch (Error $e) { echo $e->getMessage(), "\n"; }
    $q = &$foo->m; $q = 3; var_dump($foo->m);
    t($foo->bar); var_dump($foo->bar);
    $a = [&$foo->bar]; $a[0] = 1; var_dump($foo->bar);
    $foo->inside();
}
test();

// An ArrayAccess element fetched for a write through it is offsetGet
// alone; only isset asks offsetExists first.
class Inner { public $onBaz = []; public $p; }
class AA implements ArrayAccess {
    public $b;
    function __construct() { $this->b = new Inner; }
    function offsetGet($k): mixed { echo "get($k)\n"; return $this->b; }
    function offsetSet($k, $v): void { echo "set($k)\n"; }
    function offsetUnset($k): void { echo "unset($k)\n"; }
    function offsetExists($k): bool { echo "exists($k)\n"; return true; }
}
$aa = new AA;
echo "-- append\n"; $aa["x"]->onBaz[] = 1;
echo "-- set\n"; $aa["x"]->p = 2;
echo "-- concat\n"; $aa["x"]->p .= "z";
echo "-- inc\n"; $aa["x"]->p++;
echo "-- isset\n"; var_dump(isset($aa["x"]->p));
echo "-- unset\n"; unset($aa["x"]->p);
echo "-- ref\n"; $r = &$aa["x"]->onBaz;
var_dump($aa->b->onBaz, property_exists($aa->b, "p"));

// (array) of an object with a shadowed parent private names each slot
// once: `"\0P\0x"` and `"\0C\0x"`.
class P2 { private $x = 'p'; protected $y = 'py'; }
class C2 extends P2 { private $x = 'c'; public $z = 1; }
foreach ((array) new C2 as $k => $v) { echo strlen($k), ":", str_replace("\0", '|', $k), "=", $v, "\n"; }
var_dump(array_keys(get_mangled_object_vars(new C2)) === array_keys((array) new C2));
