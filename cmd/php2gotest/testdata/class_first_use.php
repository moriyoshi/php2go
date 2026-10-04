<?php
// A class's pending initializers -- constants, then property defaults, then
// statics, the parent's first -- all run on its first `new` or first static
// property access, and the first failure is the Error. A class-constant access
// evaluates only that constant, and a static method call evaluates nothing.

class StaticOnly
{
    public static $list = ['k' => NOT_YET];
    public static function m() { return "m ran"; }
}
echo StaticOnly::m(), "\n";
try {
    new StaticOnly;
} catch (Error $e) {
    echo "new: ", $e->getMessage(), "\n";
}
define('NOT_YET', 5);
var_dump(StaticOnly::$list);
StaticOnly::$list = "written";
var_dump(StaticOnly::$list);

class Ordered
{
    const C = MISSING_C;
    public $p = MISSING_P;
    public static $s = MISSING_S;
}
try {
    new Ordered;
} catch (Error $e) {
    echo "first: ", $e->getMessage(), "\n";
}
try {
    new Ordered;
} catch (Error $e) {
    echo "again: ", $e->getMessage(), "\n";
}
define('MISSING_C', 1);
try {
    echo Ordered::$s;
} catch (Error $e) {
    echo "static access: ", $e->getMessage(), "\n";
}
define('MISSING_P', 2);
define('MISSING_S', 3);
$o = new Ordered;
var_dump(Ordered::C, $o->p, Ordered::$s);

class OnlyConst
{
    const GOOD = 1;
    const BAD = NOPE_CONST;
    public static $s = NOPE_STATIC;
}
var_dump(OnlyConst::GOOD);

class ParentWithBad
{
    public static $s = NOPE_PARENT;
}
class ChildOfBad extends ParentWithBad
{
    public $x = 1;
}
try {
    new ChildOfBad;
} catch (Error $e) {
    echo "child: ", $e->getMessage(), "\n";
}

class TypedConst
{
    public const self ME = UNDEFINED_ME;
}
try {
    define("UNDEFINED_ME", new TypedConst());
} catch (Error $e) {
    echo "typed: ", $e->getMessage(), "\n";
}
