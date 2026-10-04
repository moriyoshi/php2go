<?php
// A constant whose initializer names a class being autoloaded is not "in
// progress" until that class exists: an autoloader that reads the very
// constant back evaluates it to completion. A constant that really reaches
// itself is still refused.

class B { const C = A::C . "B"; }
class Loop { const X = self::Y; const Y = self::X; }

spl_autoload_register(function ($class) {
    if ($class === 'A') {
        class A { const C = "A"; }
        var_dump(B::C);
    }
});

var_dump(B::C);
try { var_dump(Loop::X); } catch (Error $e) { echo $e->getMessage(), "\n"; }
