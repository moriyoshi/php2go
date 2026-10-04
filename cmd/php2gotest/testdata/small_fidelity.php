<?php
namespace App;

// A round of small divergences, each measured against the engine.

// `~` has a complement only for a string and the numbers.
class Thing {}
foreach ([null, true, false, [], [1], new Thing, 5, 1.0, "ab"] as $v) {
    try {
        var_dump(~$v);
    } catch (\TypeError $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

// `$this` cannot be written by name, inside a class or not.
function reassign()
{
    $name = "this";
    try {
        $$name = 0;
    } catch (\Error $e) {
        echo $e->getMessage(), "\n";
    }
    var_dump(isset($$name));
}
reassign();
$global = "this";
try {
    $$global = 1;
} catch (\Error $e) {
    echo "global: ", $e->getMessage(), "\n";
}
function viaGlobals()
{
    $n = "this";
    try {
        $GLOBALS[$n] = 2;
    } catch (\Error $e) {
        echo "\$GLOBALS: ", $e->getMessage(), "\n";
    }
}
viaGlobals();

// A namespaced class name is quoted as written.
try {
    echo Missing::CONSTANT;
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
$nothing = null;
try {
    $nothing->{"odd\\name"} = 1;
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    $nothing->{"odd\\name"}++;
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}

// A negative string offset past the start is reported as written.
$s = "abc";
var_dump($s[-10], $s[-1]);

// Callables: an unreachable method reaches __call; a static callable inside a
// method borrows $this; `[$obj, "Ancestor::m"]` is deprecated and dispatched.
class Magic
{
    protected function prot() { echo "prot ran\n"; }
    public function __call($name, $args) { echo "__call($name)\n"; }
    public static function __callStatic($name, $args) { echo "__callStatic($name)\n"; }
    public function inside()
    {
        call_user_func([__NAMESPACE__ . '\MAGIC', 'viaStatic']);
        call_user_func([$this, 'prot']);
    }
}
call_user_func([new Magic, 'prot']);
(new Magic)->inside();
call_user_func([__NAMESPACE__ . '\Magic', 'fromOutside']);
$class = __NAMESPACE__ . '\MAGIC';
$class::dynamicStatic();

class Base
{
    public function who() { return static::class . " via Base::who"; }
}
class Derived extends Base
{
    public function who() { return "Derived::who"; }
}
var_dump(call_user_func([new Derived, __NAMESPACE__ . '\Base::who']));

// Constants whose initializers reach each other.
class Cycle
{
    const A = [...self::B];
    const B = [...self::A];
    const OK = [1, ...self::LATER];
    const LATER = [2];
}
var_dump(Cycle::OK);
try {
    var_dump(Cycle::A);
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}

// A class declared where it runs, with an interface that does not exist,
// fails there. (A missing parent is refused by the emitter outright.)
try {
    if (true) {
        class HasMissingInterface implements NoSuchInterface {}
    }
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
// An engine interface is always there.
if (true) {
    class Countable implements \Countable, \ArrayAccess
    {
        function count(): int { return 3; }
        function offsetExists($o): bool { return false; }
        function offsetGet($o): mixed { return null; }
        function offsetSet($o, $v): void {}
        function offsetUnset($o): void {}
    }
}
echo count(new Countable), "\n";

// Returning by reference: a variable is a reference, anything else notices.
function &byRefLocal($x) { return $x; }
function &byRefLiteral() { return 5; }
function &byRefTemp($x) { return $x + 1; }
function &byRefAssign($x) { return $x = 7; }
function &byRefCall() { return strtoupper("c"); }
function &byRefRefCall() { return byRefLocal("r"); }
function &byRefImplicit() {}
var_dump(byRefLiteral(), byRefTemp(1), byRefAssign(1), byRefCall(), byRefRefCall(), byRefImplicit());
var_dump(array_pop(byRefLocal([1, 2, 3])));
function &gen() { $v = 1; yield $v; }
function &genOuter() { return gen(); }
foreach (genOuter() as $v) {
    var_dump($v);
}

// A `&__get` hands back its local, and a compound assignment writes into it.
class Getter
{
    private $hidden = 1;
    public function &__get($name)
    {
        $value = 10;
        return $value;
    }
    public function __set($name, $value) { echo "__set($name)\n"; }
}
$g = new Getter;
$g->p += 1;
var_dump($g->p);
$g->hidden += 1;
var_dump($g->hidden);

// A namespaced count() falls back to the library function, which must answer
// as the opcode does: a Countable's own count, a TypeError for a scalar.
class Five implements \Countable
{
    function count(): int { return 5; }
}
var_dump(count(new Five), count([1, 2]));
try {
    count(5);
} catch (\TypeError $e) {
    echo $e->getMessage(), "\n";
}
