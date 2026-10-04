<?php
// `self::`, `parent::` and `static::` forward the called class; naming a class
// resets it. Their callable spellings forward too (deprecated, but forwarded).
class A
{
    public static function who() { return static::class; }
}
class B extends A
{
    public static function forward()
    {
        return [parent::who(), self::who(), static::who(),
            @call_user_func("parent::who"), @call_user_func(["self", "who"])];
    }
    public static function reset()
    {
        return [A::who(), B::who(), call_user_func("A::who")];
    }
    public function fromInstance()
    {
        return [parent::who(), self::who()];
    }
}
class C extends B {}
var_dump(C::forward(), C::reset(), (new C)->fromInstance());

// A method named by something other than a string is refused.
class M
{
    public function __call($n, $a) { return "call $n"; }
    public static function __callStatic($n, $a) { return "static $n"; }
}
$o = new M;
foreach ([1, null, 1.5, ["m"]] as $name) {
    try {
        $o->$name();
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
    try {
        M::$name();
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
$good = "dyn";
var_dump($o->$good(), M::$good());

// A missing method reached through a forwarding call goes to __callStatic
// with the called class still the caller's.
class MagicBase
{
    static function who() { return static::class; }
    static function __callStatic($name, $args) { return "$name via " . static::who(); }
}
class MagicChild extends MagicBase
{
    static function run() { return [parent::missing(), self::gone(), MagicBase::direct()]; }
}
var_dump(MagicChild::run());
