<?php
// An **object** standing where a class name goes names its own class. PHP
// accepts an instance there, and reading the value as a string instead gave the
// empty name -- so `$o::$foo` reported `Access to undeclared static property
// ::$foo` and `$o::m()` a call to a method of nothing.
//
// The string form already worked, which is why this survived: the two look
// alike and only one of them was tested.
class C {
    const K = "const";
    public static $foo = "static";
    public static function m() { return "method"; }
    public function inst() { return "inst"; }
}

$o = new C();
echo $o::K, "|", $o::$foo, "|", $o::m(), "\n";

$n = "C";
echo $n::K, "|", $n::$foo, "|", $n::m(), "\n";

echo C::K, "|", C::$foo, "|", C::m(), "\n";

// Writing through an instance reaches the same slot as the class name does.
$o::$foo = "written";
echo C::$foo, "|", $n::$foo, "\n";

// An anonymous class reached through the variable that holds it, which is the
// only way to name one at all.
$a = new class("cats") {
    public static $v;
    public function __construct($v) { static::$v = $v; }
    public static function get() { return static::$v; }
};
var_dump($a::$v);
var_dump($a::get());
