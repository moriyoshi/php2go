<?php
// An argument fetched for a callee known only at run time: a dimension or
// property of a temporary has no storage, so a by-reference parameter refuses
// it (`Cannot use temporary expression in write context`), even nested; a
// by-value one reads it, and reading a property of a non-object warns.

$byRef = function (&$ref) { echo "bound\n"; };
$byVal = function ($v) { var_dump($v); };
function f() { return [1]; }
function o() { $o = new stdClass; $o->p = 7; return $o; }
$a = [1];
$s = "x";
$n = null;
$i = 3;

$cases = [
    'const prop' => fn($c) => $c([0, 1]->prop),
    'const dim' => fn($c) => $c([0, 1][0]),
    'tmp prop' => fn($c) => $c(($a + [])->prop),
    'tmp dim' => fn($c) => $c(($a + [])[0]),
    'concat dim' => fn($c) => $c(($s . "y")[0]),
    'call dim' => fn($c) => $c(f()[0]),
    'call prop' => fn($c) => $c(o()->p),
    'const nested dim' => fn($c) => $c([[1]][0][0]),
    'null prop' => fn($c) => $c($n->p),
    'int prop' => fn($c) => $c($i->p),
    'array prop' => fn($c) => $c($a->p),
    'element prop' => fn($c) => $c($a[0]->p),
];
foreach ([$byRef, $byVal] as $callee) {
    foreach ($cases as $k => $case) {
        echo "$k: ";
        try {
            $case($callee);
        } catch (Error $e) {
            echo get_class($e), ": ", $e->getMessage(), "\n";
        }
    }
}
