<?php
// A builtin reached as a **callable** owes the same argument checks as one
// written as a call. `callValue` looked the function up and invoked it
// directly, skipping `callBuiltin` and with it every verification -- so a
// callback invoked with the wrong count simply ran.
//
// `ob_start`'s filter is the sharpest case: PHP calls it with **two** arguments
// (buffer, phase), so a one-parameter function raises ArgumentCountError there
// and nowhere else in the program.
function t(callable $f): void {
    try { var_dump($f()); }
    catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}

// Through call_user_func and friends.
t(fn() => call_user_func("strtoupper", "a", "b"));
t(fn() => call_user_func_array("strlen", ["ab", 9]));
t(fn() => call_user_func("strtoupper"));

// As a callback given to another builtin.
t(fn() => array_map("strtoupper", ["a"]));      // correct arity: fine
$sortable = ["b", "a"];
t(function () use (&$sortable) { usort($sortable, "strcmp"); return $sortable; });

// A closure callable is unaffected.
t(fn() => array_map(fn($s) => strtoupper($s), ["a", "b"]));

// Type checks still apply through a callable too.
t(fn() => call_user_func("str_repeat", "a", []));
echo "done\n";
