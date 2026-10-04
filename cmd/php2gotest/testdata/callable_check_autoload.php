<?php
// Checking a callback that names a class autoloads the class: an autoloader
// that throws makes its exception the answer, rather than the TypeError about
// an invalid callback; one that declares the class makes the callback valid.

spl_autoload_register(function ($class) {
    echo "autoload $class\n";
    if ($class === 'Good') {
        class Good { static function up($s) { return strtoupper($s); } }
        return;
    }
    throw new Exception("Failed $class");
});
try {
    array_map('A::b', []);
} catch (Exception $e) {
    echo $e->getMessage(), "\n";
}
var_dump(array_map('Good::up', ['x']));
