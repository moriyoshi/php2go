<?php
// A class declared **inside a function** is bound when that declaration runs,
// not before -- so `new B` reaches the autoloader, and the autoloader's own
// `class B extends A` links B at its declaration point, which autoloads A from
// inside the load of B.
spl_autoload_register(function ($class) {
    echo "autoload $class\n";
    if ($class === 'A') {
        class A {}
        echo "declared A\n";
    } elseif ($class === 'B') {
        class B extends A {}
        echo "declared B\n";
    } elseif ($class === 'I') {
        interface I {}
        echo "declared I\n";
    } elseif ($class === 'C') {
        class C implements I {}
        echo "declared C\n";
    }
});
var_dump(get_class(new B));
var_dump(get_class(new C));

// A class declared in a branch is not bound until the branch runs, with no
// autoloader involved.
if (true) {
    class Later {}
}
var_dump(get_class(new Later));

// `class_alias`'s third argument is autoload, defaulting to true: aliasing a
// conditionally declared class has to load it first.
spl_autoload_register(function ($c) { if ($c === 'Aliased') { class Aliased {} } });
var_dump(class_alias('Aliased', 'AliasName', true));
var_dump(get_class(new AliasName));

// Linking variance-checks each override against what it overrides, which
// resolves the class names in **both** signatures -- the child's first. A
// method the parent does not declare is never checked, so its types are never
// loaded: `other(): S` below leaves S alone.
spl_autoload_register(function ($c) {
    echo "link $c\n";
    if ($c === 'P') { class P { public function m(): Q {} } }
    elseif ($c === 'Q') { class Q {} }
    elseif ($c === 'R') { class R extends Q {} }
    elseif ($c === 'S') { class S {} }
    elseif ($c === 'Kid') { class Kid extends P { public function m(): R {} public function other(): S {} } }
    echo "bound $c\n";
});
echo get_class(new Kid), "\n";
var_dump(class_exists('S', false));
