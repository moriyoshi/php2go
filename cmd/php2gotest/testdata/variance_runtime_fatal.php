<?php
// An override that turns out incompatible once its classes are loaded is
// the engine's fatal, on the overriding method's line, as soon as the class
// it needed is there -- before anything else it named is loaded.
register_shutdown_function(function () {
    echo "shutdown\n";
});
spl_autoload_register(function ($class) {
    echo "load $class\n";
    if ($class == 'A') {
        class A {
            function m(): B {}
            function m2(): B {}
        }
    } elseif ($class == 'B') {
        class B extends A {
            function m(): X {}
            function m2(): Y {}
        }
    } elseif ($class == 'X') {
        class X extends A {}
    } else {
        class Y extends B {}
    }
    echo "declared $class\n";
});
new B;
