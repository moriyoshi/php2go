<?php
// `C::__construct()` is compiled as a constructor call, not a call to a
// method named `__construct`: a class with no constructor is `Cannot call
// constructor` even when it has `__callStatic`, while one that inherits a
// constructor calls it.

class OnlyMagic
{
    static function __callStatic($a, $b) { var_dump($a); }
}
try {
    OnlyMagic::__construct();
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}

class Base
{
    public $made = 0;
    function __construct() { $this->made++; echo "Base ctor\n"; }
}
class Child extends Base
{
    function again() { Child::__construct(); return $this->made; }
}
$c = new Child;
var_dump($c->again());

class NoCtor
{
    function m() { self::__construct(); }
}
try {
    (new NoCtor)->m();
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}

// The class is fetched before the method is looked up, however the method
// is named.
try {
    Missing::foobar();
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
$str = "foo";
try {
    Missing::{$str . "bar"}();
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
spl_autoload_register(function ($c) {
    echo "autoload($c)\n";
});
try {
    Absent::m();
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
