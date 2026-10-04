<?php
// **A variadic parameter collects what no parameter claimed, under its name.**
// The argument slice is positional and cannot carry the key, so the names travel
// beside it -- they are always the trailing arguments, because a positional
// argument after a named one does not compile.
function v($a, ...$rest) { var_dump($a, $rest); }
v(1, 2, 3);
v(1, x: "X", y: "Y");

// A named argument may claim a declared parameter while the others go to the
// variadic.
v(a: 1, z: "Z");

// A wholly variadic callee, positional and named together.
function w(...$all) { var_dump($all); }
w(1, k: "K");
w(p: 1, q: 2);

// The frame records how the arguments arrived, which is what a trace renders
// and what debug_backtrace() keys its args array by.
function traced($a, ...$rest) { var_dump(debug_backtrace()[0]["args"]); }
traced(1, 2, x: 3, y: 4);

// A declared type applies to each collected element whichever way it arrived.
function typed(int ...$nums) { var_dump($nums); }
typed(1, second: 2);
try { typed(1, bad: "x"); } catch (TypeError $e) { echo get_class($e), "\n"; }

// **A name never claims the variadic parameter itself.** `test(extra: "x")`
// against `function test($a, string ...$extra)` puts "x" into the collected
// array under "extra" -- the parameter has no single position to bind.
//
// And an extra named argument is not one of the call's arguments as far as
// func_num_args() and func_get_args() are concerned.
function named($a, string ...$extra) {
    var_dump($a, $extra, func_num_args(), func_get_args());
}
named(b: "b", a: "a", c: "c", extra: "extra");
named("a", "b", "c", d: "d");
