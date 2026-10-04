<?php
// `function f() {}` inside an `if` is a **declaration instruction**, not a
// definition: the engine emits ZEND_DECLARE_FUNCTION where the source put it,
// and the function does not exist until that runs. The harvest keeps such a
// body among the *closures*, because that is where a nested op_array goes, so
// the unit's function list holds only the unconditional ones.

// Calling one before its declaration has run is the program's error.
try { later(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

if (true) {
    function later($a = "default") { return "later($a)"; }
}
var_dump(later(), later("given"));

// A declaration on a branch that never runs never declares anything.
if (false) {
    function never() { return "never"; }
}
try { never(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// Declared inside a function body, so it exists only once that function is
// called -- and calling it twice is a fatal redeclaration, so only once here.
function declarer() {
    function inner() { return "inner"; }
    return "declared";
}
try { inner(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump(declarer());
var_dump(inner());

// A conditional function is an ordinary callable once it exists.
$f = "later";
var_dump($f("through a variable"));

// `function_exists` is the question the wrapper answers, and it cannot come
// from a compile-time table: the same source is false before its branch runs
// and true after. A library function counts too, including one compiled to an
// opcode (`strlen`) and one this compiler does not implement (`array_map`) --
// PHP says both exist, and existing is the question being asked.
var_dump(function_exists("later"), function_exists("LATER"), function_exists("never"));
var_dump(function_exists("strlen"), function_exists("printf"), function_exists("array_map"));
var_dump(function_exists("get_class"), function_exists("nope"), function_exists(""));

// It sees the same globals every function does.
$g = "global value";
if (true) {
    function readsGlobal() { global $g; return $g; }
}
var_dump(readsGlobal());
