<?php
// Reaching a static member on a trait itself, rather than on a class using
// it, is deprecated at every access -- property reads, writes and steps,
// method calls including one __callStatic answers -- while the using
// class's own name is not.

trait T
{
    public static $foo = 0;
    public static function foo() { return "foo"; }
    public static function __callStatic($name, $args) { return "callStatic($name)"; }
}
class C { use T; }

function test()
{
    T::$foo = 42;
    T::$foo++;
    var_dump(T::$foo, T::foo(), T::bar());
}
test();
test();
C::$foo = 1;
var_dump(C::$foo, C::foo(), C::baz());
