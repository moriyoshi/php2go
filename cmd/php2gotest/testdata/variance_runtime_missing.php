<?php
// A class an override names that no autoloader provides: `Could not check
// compatibility`.
spl_autoload_register(function ($class) {
    echo "load $class\n";
    if ($class == 'A') {
        class A {
            function m($a): B {}
        }
    } elseif ($class == 'B') {
        class B extends A {
            function m($a): Nowhere {}
        }
    }
});
new B;
