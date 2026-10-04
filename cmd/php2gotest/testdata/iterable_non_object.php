<?php
// A value that is not an object is decidably outside a type made only of
// classes, `array` and `null` -- `iterable` is `Traversable|array` -- and
// outside Traversable, Generator and Closure alone.

function it(): iterable { return 1; }
function maybe(): ?iterable { return null; }
function gen(): Generator { return [1]; }
function trav(?Traversable $t) { return "ok"; }

foreach ([
    fn() => it(),
    fn() => var_dump(maybe()),
    fn() => gen(),
    fn() => trav("string"),
    fn() => trav(null),
    fn() => var_dump((function (): iterable { return [1]; })()),
] as $f) {
    try {
        var_dump($f());
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}

$o = new class {
    public ?Traversable $t = null;
    public iterable $i = [];
};
try {
    $o->t = [];
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
try {
    $o->i = "no";
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump($o->t, $o->i);
