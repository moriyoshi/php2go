<?php
// Literal call sites and class-constant sites keep what they resolved
// (OWNERSHIP-PERF.md, call sites). Each shape here is one where a remembered
// answer would be wrong: a site that sees receivers of different classes, a
// method only some scopes may call, `__call`, an engine object, an instance
// method reached with static syntax, late static binding, a constant that
// notices on every read, an array constant, a class that does not exist yet.

class A
{
    public function who() { return "A"; }
    private function secret() { return "A-secret"; }
    public function callSecret($o) { return $o->secret(); }
    public static function make() { return static::class; }
    public static function name() { return "A::name"; }
    public function inst() { return "inst of " . static::class; }
    public function viaStatic() { return A::inst(); }
    public function viaParentName() { return self::name(); }
}

class B extends A
{
    public function who() { return "B"; }
}

class C
{
    public function who() { return "C"; }
    public function __call($n, $a) { return "C::__call($n)"; }
}

class M
{
    public function __call($n, $a) { return "M::__call($n)"; }
}

enum Suit: string
{
    case Hearts = "H";
    case Spades = "S";
}

class K
{
    const LIST = [1, 2];
    const SCALAR = 7;
    #[\Deprecated("use SCALAR")]
    const OLD = 1;
}

function who($o)
{
    try {
        return $o->who();
    } catch (Error $e) {
        return get_class($e) . ": " . $e->getMessage();
    }
}

// One site, receivers of four kinds, then the first kind again.
foreach ([new A, new B, new C, new M, new A, new B] as $o) {
    echo who($o), "\n";
}
$f = function () { return "closure"; };
echo who($f), "\n";

// A private method: callable from A's scope, `__call` from outside when the
// class has one, an Error when it does not.
function secretOf($o)
{
    try {
        return $o->secret();
    } catch (Error $e) {
        return get_class($e) . ": " . $e->getMessage();
    }
}
echo (new A)->callSecret(new A), "\n";
echo (new A)->callSecret(new B), "\n";
echo secretOf(new A), "\n", secretOf(new A), "\n";

// Static calls: late static binding through one site, an inherited static,
// an instance method with static syntax, the same site used twice.
function make($which)
{
    return $which === "A" ? A::make() : B::make();
}
echo make("A"), " ", make("B"), " ", make("A"), "\n";
echo B::name(), " ", B::name(), "\n";
echo (new B)->viaStatic(), " ", (new A)->viaStatic(), "\n";
echo (new B)->viaParentName(), "\n";
function staticInst()
{
    try {
        return A::inst();
    } catch (Error $e) {
        return get_class($e) . ": " . $e->getMessage();
    }
}
echo staticInst(), "\n", staticInst(), "\n";

// Constants through one site each.
function suit() { return Suit::Hearts; }
var_dump(suit() === suit(), suit()->value);
function list_() { return K::LIST; }
$l = list_();
$l[] = 3;
var_dump(count($l), count(list_()), count(K::LIST));
// Written in place through the same site, twice: each read is its own array.
function growList()
{
    $l = K::LIST;
    $l[] = 3;
    $l[0] = 9;
    return $l;
}
var_dump(growList(), growList(), K::LIST);
function old() { return K::OLD; }
var_dump(old(), old());
function scalar() { return K::SCALAR; }
var_dump(scalar() + scalar());

// A class that is declared only when the branch runs.
function lateConst()
{
    try {
        return Late::X;
    } catch (Error $e) {
        return get_class($e) . ": " . $e->getMessage();
    }
}
function lateCall()
{
    try {
        return Late::hello();
    } catch (Error $e) {
        return get_class($e) . ": " . $e->getMessage();
    }
}
var_dump(lateConst(), lateCall());
if (K::SCALAR > 0) {
    class Late
    {
        const X = "x";
        public static function hello() { return "hello"; }
    }
}
var_dump(lateConst(), lateCall(), lateConst(), lateCall());
echo "end\n";
