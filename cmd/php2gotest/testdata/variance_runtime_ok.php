<?php
// Overrides of classes declared at run time are checked once the classes
// their signatures name are loaded: compatible ones pass, a class still
// being declared is waited for, and an engine interface is the engine's.
spl_autoload_register(function ($class) {
    echo "load $class\n";
    if ($class === 'A') {
        class A {
            public function ret(): B {}
            public function arg(C $c) {}
            public function str(): Stringable {}
        }
    } elseif ($class === 'B') {
        class B extends A {
            public function ret(): C {}
            public function arg(B $b) {}
            public function str(): B {}
            public function __toString(): string { return "b"; }
        }
    } else {
        class C extends B {}
    }
});
var_dump(new C);
echo "done\n";
