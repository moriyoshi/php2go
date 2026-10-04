<?php
// A temporary dies when the expression that made it is done with it, and
// the next object takes its handle: a closure made only to be dumped
// (`f(...)`, `fn() => ...`), and the operand of `clone new X`, which leaves
// only the copy (first_class_callable_optimization, objects_031).

function t1() {}
var_dump(t1(...));
var_dump(t1(...));
var_dump(fn() => 1);
var_dump(new stdClass);

$a = clone new stdClass;
$b = clone new stdClass;
var_dump($a, $b);

// Kept, it keeps its number.
$f = t1(...);
var_dump(t1(...), $f);
