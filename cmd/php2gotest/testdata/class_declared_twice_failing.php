<?php
// A class declared more than once where no declaration can succeed: each
// names a parent or interface nothing can make exist. Each site runs its own
// link -- parent first, then interfaces, autoloading -- and fails there,
// catchably; once a variance check made while it was linking depends on it,
// the failure is the engine's fatal (probed; variance/loading_exception1).

interface Known {}

for ($i = 0; $i < 2; $i++) {
    try {
        class B extends Missing1 {}
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
try {
    class B implements Known, Missing2 {}
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
var_dump(class_exists('B', false));

spl_autoload_register(function ($c) {
    echo "load $c\n";
});
try {
    class B extends Missing1 implements Known {}
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

spl_autoload_register(function ($c) {
    throw new RuntimeException("no $c");
});
try {
    class B implements Missing2 {}
} catch (Exception $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

spl_autoload_register(function ($c) {
    echo "tie $c\n";
    class X { public function t(): Known {} }
    class Y extends X { public function t(): B {} }
}, true, true);

try {
    class B extends Missing1 {}
} catch (Exception $e) {
    echo "not reached\n";
}
echo "not reached\n";
