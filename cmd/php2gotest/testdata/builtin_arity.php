<?php
// The engine's argument-count check for library functions, which compiled
// output did not perform at all: it verified argument *types* and never the
// count, so `strtoupper("a", "b")` quietly answered "AB".
//
// Three wordings, and the choice between them is not the obvious one:
// `exactly` only when the parameter list is fixed and fully required, `at
// least` for too few, `at most` for too many -- and the number named is the
// bound that was missed, singular for 1.
function t(callable $f): void {
    try { var_dump($f()); }
    catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}

t(fn() => strtoupper("a", "b"));        // exactly 1, 2 given
t(fn() => strtoupper());                // exactly 1, 0 given
t(fn() => strlen("ab", 9));             // exactly 1, 2 given
t(fn() => str_repeat("a"));             // exactly 2, 1 given
t(fn() => substr("abc"));               // at least 2, 1 given
t(fn() => implode());                   // at least 1, 0 given
t(fn() => count([1], 0, 9));            // at most 2, 3 given

// A function reimplemented in PHP under internal/userland still refuses excess
// arguments, and in the *builtin's* words -- its own prologue only checks for
// too few, because an ordinary PHP function accepts extras.
t(fn() => str_pad("a", 2, "b", STR_PAD_RIGHT, 9));
t(fn() => ucfirst("ab", 9));

// Variadics have no maximum, and an optional parameter is not a minimum.
t(fn() => array_merge());
t(fn() => array_merge([1], [2], [3]));
t(fn() => str_pad("a", 3));
t(fn() => substr("abcdef", 1, 2));

// The check runs before the type check: too many arguments is a count error
// even when one of them is also the wrong type.
t(fn() => count([1], "not-an-int", 9));
echo "done\n";
