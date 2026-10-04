<?php
// Code eval'd in a method runs in that method's class: self, its private
// members, and static as the class the method was called on. With no
// class at all, self::class in a constant is an Error.
class C {
    const FOO = 1;
    private static $bar = 2;
    public static function f() {
        eval('var_dump(self::FOO, self::$bar, self::class, static::class);');
    }
    public function g() {
        eval('const IN_METHOD = self::class; var_dump(IN_METHOD);');
    }
}
class D extends C {}
C::f();
D::f();
(new C)->g();
try {
    eval('const OUTSIDE = self::class;');
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
const TOP = self::class;
echo "not reached\n";
