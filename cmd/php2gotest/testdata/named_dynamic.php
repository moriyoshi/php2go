<?php
// **The callee places a named argument the call site could not.** A call whose
// callee is unknown at compile time -- a first-class callable, a variable
// holding a closure, `__call` -- sends its named arguments trailing under their
// names, because only the callee knows which position each one claims.
function f(string $a = "a", string $b = "b", string $c = "c") { echo "$a|$b|$c\n"; }
$g = f(...);
$g(c: "C");
$g("A", c: "C");
$g(b: "B", c: "C");

// The gap a skipped parameter leaves is "not passed", so its default applies --
// the same thing a short argument list means.
$h = function ($x = 1, $y = 2) { echo "$x/$y\n"; };
$h(y: 9);

// A name no parameter claims, with no variadic to collect it, is an Error the
// program can catch -- and nobody but the callee can report it, because the call
// site had no signature to check against.
try { $h(nope: 1); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A variadic does collect it, under its name.
$v = function ($first, ...$rest) { var_dump($first, $rest); };
$v(1, tail: "T");

// **A name never claims the variadic parameter itself**, whichever side places
// it: the call site resolves what it can, and what reaches the callee has to
// follow the same rule.
$w = function ($head, ...$extra) { var_dump($head, $extra); };
$w(1, extra: "E");
$named = "w";
${$named} = $w;
${$named}(head: 2, other: "O");
