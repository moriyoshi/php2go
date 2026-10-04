<?php
// The types a class's overrides name are loaded in the order of the
// parent's method table, not the child's; and a class declared while those
// loads are under way first loads the rest of them.
spl_autoload_register(function ($class) {
    echo "load $class\n";
    if ($class == 'P') {
        class P {
            function second(): P {}
            function first(): P {}
        }
    } elseif ($class == 'C') {
        class C extends P {
            function first(): T1 {}
            function second(): T2 {}
        }
        echo "made C\n";
    } elseif ($class == 'T1') {
        class T1 extends C {}
        echo "made T1\n";
    } elseif ($class == 'T2') {
        class T2 extends C {}
        echo "made T2\n";
    }
});
new C;
echo "done\n";
