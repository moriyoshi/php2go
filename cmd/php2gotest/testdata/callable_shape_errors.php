<?php
// How a malformed call is refused, each shape in its own words:
// call_user_func_array takes only an array as its argument list, where a
// written `...$x` also takes a Traversable and says `Only arrays and
// Traversables can be unpacked` for anything else; an array callback needs
// indices 0 and 1 before either is looked at (bug63173).

class C { function m(...$a) { return count($a); } }

function attempt(callable|Closure $f) {
    try {
        var_dump($f());
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
$c = [new C, 'm'];
attempt(fn() => call_user_func_array($c, null));
attempt(fn() => call_user_func_array($c, 5));
attempt(fn() => call_user_func_array($c, new ArrayIterator([1, 2])));
attempt(fn() => call_user_func_array($c, [1, 2, 3]));
$n = null;
attempt(fn() => (new C)->m(...$n));
attempt(fn() => (new C)->m(...new ArrayIterator([1, 2])));

foreach ([[1 => 0, 2 => 0], [0 => 'C'], ['C', 'm', 'x'], [1, 'm'], ['C', 5]] as $cb) {
    attempt(fn() => $cb());
}
