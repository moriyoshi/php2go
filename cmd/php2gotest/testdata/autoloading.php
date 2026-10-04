<?php
// A compiled program has no files to load, so the only way an autoloader can
// make a class exist here is `class_alias`. Everything else about autoloading
// is the same, and it is what the tests are about: **which** callbacks run, in
// what order, and that the chain stops as soon as the class exists.

class Real { public $tag = "real"; }

$log = [];
spl_autoload_register(function ($n) use (&$log) {
    $log[] = "A($n)";
    if ($n === "Aliased") { class_alias("Real", "Aliased"); }
});
spl_autoload_register(function ($n) use (&$log) { $log[] = "B($n)"; });

// Both run for a name nothing defines, in registration order.
var_dump(class_exists("Nope"));
var_dump($log);

// The chain stops as soon as the class exists: only A runs here.
$log = [];
var_dump(class_exists("Aliased"));
var_dump($log);
$obj = new Aliased;
var_dump(get_class($obj), $obj->tag);

// The second argument decides whether to autoload at all, and defaults to true.
$log = [];
var_dump(class_exists("AlsoNope", false));
var_dump($log);

// An autoloader asking about the name it was called with does not re-run the
// chain -- without the guard that is an infinite recursion.
$log = [];
spl_autoload_register(function ($n) use (&$log) {
    $log[] = "C($n)";
    class_exists($n);
    $log[] = "C done";
});
var_dump(class_exists("Recur"));
var_dump($log);

// The registered list, and removing one.
var_dump(count(spl_autoload_functions()));
$extra = function ($n) { };
spl_autoload_register($extra);
var_dump(count(spl_autoload_functions()));
var_dump(spl_autoload_unregister($extra));
var_dump(count(spl_autoload_functions()));
var_dump(spl_autoload_unregister($extra));

// `prepend` puts one at the front.
$log = [];
spl_autoload_register(function ($n) use (&$log) { $log[] = "front($n)"; }, true, true);
class_exists("Ordering");
var_dump($log[0]);

// spl_autoload_call runs the chain without asking about the class.
$log = [];
spl_autoload_call("Called");
var_dump(in_array("front(Called)", $log));
