<?php
// A class whose declaration is under way is known to a variance check: its
// declared ancestry answers it, and it is not autoloaded again.
interface I {}
spl_autoload_register(function ($class) {
    echo "load $class\n";
    class X {
        function test(): I {}
    }
    class Y extends X {
        function test(): C {}
    }
});
class C extends Z implements C {}
