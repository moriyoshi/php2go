<?php
// An arrow function captures only the variables that are defined when it is
// made: an undefined one is not bound, and not listed under `static`; with
// none bound there is no `static` at all (bug81076). An explicit `use` of an
// undefined variable warns and captures NULL.

$x = 1;
var_dump(fn() => [$x, $undefined]);
print_r(fn() => [$nope, $nada]); echo "\n";
print_r(function () use ($missing) { return $missing; }); echo "\n";
