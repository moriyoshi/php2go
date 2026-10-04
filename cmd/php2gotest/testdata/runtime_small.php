<?php
// A handful of small divergences, each measured against the engine.

// define() refuses a class constant's name, `::` alone included.
foreach (['foo::bar', '::'] as $name) {
    try {
        define($name, 1);
    } catch (ValueError $e) {
        echo $e->getMessage(), "\n";
    }
}
var_dump(define('with:colon', 2), constant('with:colon'));

// An unhandled match names an enum case as written.
enum Suit { case Hearts; case Spades; }
try {
    echo match (Suit::Spades) { Suit::Hearts => "h" };
} catch (UnhandledMatchError $e) {
    echo $e->getMessage(), "\n";
}
try {
    echo match (new stdClass) { 1 => "one" };
} catch (UnhandledMatchError $e) {
    echo $e->getMessage(), "\n";
}

// A `callable` parameter refuses what is not a valid callback.
class Holder
{
    function instanceMethod() {}
    static function staticMethod() {}
}
function wantsCallable(callable $f) { return gettype($f); }
function maybeCallable(?callable $f) { return gettype($f); }
var_dump(wantsCallable('strlen'), wantsCallable(['Holder', 'staticMethod']), maybeCallable(null));
foreach ([['Holder', 'instanceMethod'], 'no_such_function', 5] as $bad) {
    try {
        wantsCallable($bad);
    } catch (TypeError $e) {
        echo explode(', called in', $e->getMessage())[0], "\n";
    }
}

// A readonly property's first write may come from any related class
// (protected(set) since PHP 8.4), and not from outside.
class Base
{
    public readonly int $value;
    public function initFromBase() { $this->value = 1; }
}
class Derived extends Base
{
    public function initFromDerived() { $this->value = 2; }
}
$d = new Derived;
$d->initFromDerived();
var_dump($d->value);
$d2 = new Derived;
$d2->initFromBase();
var_dump($d2->value);
try {
    $outside = new Derived;
    $outside->value = 3;
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}

// `static::` in a closure is the called class where the closure was made,
// also for a static closure and one made in a static method.
class Named
{
    static function name() { return 'Named'; }
    function viaInstance()
    {
        $plain = function () { return static::name(); };
        $static = static function () { return static::name(); };
        return [$plain(), $static()];
    }
    static function viaStatic()
    {
        $fn = function () { return static::name(); };
        return $fn();
    }
}
class Renamed extends Named
{
    static function name() { return 'Renamed'; }
}
var_dump((new Renamed)->viaInstance(), Renamed::viaStatic(), Named::viaStatic());

// A return type coerces the value returned, not the caller's variable behind
// a by-reference parameter -- except from a function that returns by
// reference, whose reference is what goes back.
function byValue(&$n): string { return $n; }
function &byRefInt(int $n): string { return $n; }
function &byRefBoth(int &$n): string { return $n; }
$x = 123;
var_dump(byValue($x));
var_dump($x);
var_dump(byRefInt($x));
var_dump($x);
var_dump(byRefBoth($x));
var_dump($x);

// Generator::throw() takes a Throwable, and a nullable callback parameter
// says so when it refuses.
function gen() { yield 1; }
$g = gen();
try {
    $g->throw(new stdClass);
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
try {
    array_map("no_such_function", [1]);
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
try {
    usort($list, "no_such_function");
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}

// `self` in a parameter type is the class -- and in a trait, the class using
// it; a typed static property is uninitialized until something is stored.
class SelfTyped
{
    public function take(self $other) { return "took"; }
    public static int $count;
    public static ?int $maybe;
}
var_dump((new SelfTyped)->take(new SelfTyped));
try {
    (new SelfTyped)->take(new stdClass);
} catch (TypeError $e) {
    echo explode(', called in', $e->getMessage())[0], "\n";
}
trait UsesSelf
{
    public function same(self $o) { return get_class($o); }
    public function viaClosure() { return array_map(function (self $s) { return get_class($s); }, [$this]); }
}
class TraitUser { use UsesSelf; }
var_dump((new TraitUser)->same(new TraitUser), (new TraitUser)->viaClosure());
foreach (['count', 'maybe'] as $p) {
    try {
        var_dump(SelfTyped::$$p);
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
SelfTyped::$count = 3;
var_dump(SelfTyped::$count);

// A property unset() removed is __set's before its set-visibility is asked;
// binding a reference to what __get serves is refused after __get runs; an
// assignment through an array element holding a typed reference has the
// coerced value.
class Guarded
{
    public private(set) string $bar = "init";
    public function clear() { unset($this->bar); }
    public function __set($n, $v) { echo "__set($n)\n"; }
}
$gd = new Guarded;
try {
    $gd->bar = "outside";
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
$gd->clear();
$gd->bar = "after unset";
class Overloaded
{
    private $hidden;
    public function __get($n) { echo "__get($n)\n"; return 1; }
}
$ov = new Overloaded;
$target = 1;
try {
    $ov->hidden =& $target;
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
class TypedHolder { public ?string $prop; }
$th = new TypedHolder;
$arr = [];
$arr[0] =& $th->prop;
var_dump($arr[0] = 0, $th->prop);

// Binding a reference to a typed property coerces what it holds, or refuses;
// class_alias of a missing class warns; a closure's __invoke, in any case,
// binds its by-reference parameter.
class TypedBind { public static int $s; public int $i; }
$five = "5";
TypedBind::$s = &$five;
var_dump($five);
$bad = "x";
try {
    TypedBind::$s = &$bad;
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
$tb = new TypedBind;
$none = null;
try {
    $tb->i = &$none;
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump(class_alias('NoSuchClassToAlias', 'AnyAlias'));
$inc = function (&$n) { $n++; };
$count = 1;
$inc->__INVOKE($count);
$inc->__invoke($count);
var_dump($count);

// property_exists() takes object|string and converts nothing else.
foreach ([[], 1, 1.5, true, null] as $notAClass) {
    try {
        property_exists($notAClass, "p");
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}

// An object or array key in an array literal is a TypeError; an empty string
// written to a string offset has its own Error; a long unmatched string is
// escaped and cut at 15 bytes.
function literalWithKey($k) { return [$k => 1]; }
foreach ([new stdClass, [1]] as $badKey) {
    try {
        literalWithKey($badKey);
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
$str = "abc";
try {
    $str[1] = "";
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
foreach ([str_repeat("e", 20), str_repeat("e\n", 20), "tab\there"] as $subject) {
    try {
        match ($subject) {};
    } catch (UnhandledMatchError $e) {
        echo $e->getMessage(), "\n";
    }
}
