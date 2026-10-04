<?php
// ReflectionFunction of a closure answers the object it is bound to and its
// scope class: a method's closure has both, a static one only the scope,
// one rebound to a class takes that class, and a named callable made into a
// closure has neither. Return types reflect as a ReflectionNamedType, or
// null where none is declared.

class K {
    function mk() { return function () {}; }
    static function smk() { return static fn() => 1; }
    function m(): static { return $this; }
}
interface J {
    function j(): self;
}

$show = function ($f) {
    $r = new ReflectionFunction($f);
    $t = $r->getClosureThis();
    $s = $r->getClosureScopeClass();
    echo $t === null ? "null" : get_class($t), " ", $s === null ? "null" : $s->name, "\n";
};
$c = function () {};
$show($c);
$show($c->bindTo(null, K::class));
$show((new K)->mk());
$show(K::smk());
$show(Closure::fromCallable('strlen'));
$show((new K)->mk(...));
$show(Closure::bind((new K)->mk(), new K, J::class));
var_dump((new ReflectionFunction('strlen'(...)))->getClosureThis());

function f(): ?int {}
function g() {}
function v(): void {}
$arrow = fn(int $x): string => "$x";
foreach (['f', 'g', 'v', $arrow] as $n) {
    $r = new ReflectionFunction($n);
    var_dump($r->hasReturnType());
    $t = $r->getReturnType();
    echo $t === null ? "null" : get_class($t) . " " . $t . " " . var_export($t->allowsNull(), true) . " " . var_export($t->isBuiltin(), true), "\n";
}
foreach ([["K", "m"], ["K", "mk"], ["J", "j"]] as [$class, $method]) {
    $t = (new ReflectionMethod($class, $method))->getReturnType();
    echo $t === null ? "null" : $t->getName() . " " . var_export($t->isBuiltin(), true), "\n";
}

// A closure made from an inherited method is scoped to its declaring class.
class KK extends K {}
$show((new KK)->mk(...));
$show(Closure::fromCallable([new KK, 'mk']));
