<?php
class B {
    public static $v = "B-static";
    public static function who() { return "B"; }
    public static function late() { return static::who(); }
    public static function early() { return self::who(); }
}
class C extends B {
    public static function who() { return "C"; }
    public static function viaParent() { return parent::who(); }
}
echo B::late(), "\n";
echo C::late(), "\n";
echo C::early(), "\n";
echo C::viaParent(), "\n";
echo C::$v, "\n";
class T { static function boom($a) { throw new Exception("x"); } }
T::boom(7);
