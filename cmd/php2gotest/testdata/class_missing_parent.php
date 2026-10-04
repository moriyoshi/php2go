<?php
// A class whose parent nothing declares cannot be declared: the autoloader
// is asked, and the declaration is `Class "A" not found`. And a class still
// being declared is known to a variance check, not autoloaded again.
spl_autoload_register(function ($class) {
    echo "load $class\n";
});
try {
    class B extends A {
        public function m() { return 1; }
    }
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
var_dump(class_exists('B', false));
