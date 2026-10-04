<?php
class Foo {
    public static $x = 0;
    static function write($n) { for ($i = 0; $i < $n; ++$i) { self::$x = 0; } }
    static function read($n) { for ($i = 0; $i < $n; ++$i) { $x = self::$x; } }
}
$t = hrtime(true); Foo::write(5000000); Foo::read(5000000); echo "staticprop ", round((hrtime(true)-$t)/1e9, 3), "\n";
