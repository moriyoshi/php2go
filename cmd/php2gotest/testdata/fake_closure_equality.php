<?php
// Closures made from a callable compare equal when they name the same
// function with the same `$this` and scope; literal closures, however alike,
// never do.

function f() {}
class C { function m() {} static function s() {} }
class D extends C {}
$c = new C;

$pairs = [
    'f/f' => [Closure::fromCallable('f'), f(...)],
    'strlen/strlen' => [strlen(...), Closure::fromCallable('strlen')],
    'strlen/strrev' => [strlen(...), strrev(...)],
    'm on one object' => [$c->m(...), Closure::fromCallable([$c, 'm'])],
    'm on two objects' => [$c->m(...), (new C)->m(...)],
    'static C/D' => [Closure::fromCallable(['C', 's']), Closure::fromCallable(['D', 's'])],
    'rebound' => [$c->m(...), $c->m(...)->bindTo(new C)],
    'literals' => [function () {}, function () {}],
];
foreach ($pairs as $label => [$a, $b]) {
    printf("%s: %s %s\n", $label, var_export($a == $b, true), var_export($a === $b, true));
}
