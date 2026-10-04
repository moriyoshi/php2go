<?php
// An include whose path is computed runs the file the build compiled in
// for that path, resolved as the engine resolves it.
spl_autoload_register(function ($class) {
    $file = __DIR__ . "/include_computed/" . $class . ".inc";
    echo "autoload $class\n";
    require_once $file;
});

echo Alpha::hello(), "\n";
echo Beta::X, " ", get_parent_class(new Beta), "\n";

$dir = __DIR__ . "/include_computed/";
$given = "from the includer";
$r = include $dir . "ret" . ".inc";
var_dump($r, $made);

$name = "once";
var_dump(include_once $dir . $name . ".inc");
var_dump(include_once $dir . $name . ".inc");
var_dump(require_once $dir . $name . ".inc");
var_dump(include $dir . $name . ".inc");

function in_a_function($dir) {
    $local = "the function's own";
    $v = include $dir . "fn" . ".inc";
    var_dump($v, $local);
    return from_computed_include(21);
}
var_dump(in_a_function($dir));

// Through include_path, as a relative name.
set_include_path($dir);
$w = "warn";
var_dump(include "$w.inc");

// With no include_path, a relative name is looked for beside the running
// script before the working directory.
set_include_path('');
$sub = "include_computed/";
var_dump(include $sub . "sd.inc");

// A path to no file fails as a literal one does.
var_dump(@include $dir . "missing" . ".inc");

echo "done\n";
