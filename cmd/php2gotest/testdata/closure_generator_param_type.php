<?php
// `Closure` and `Generator` are final classes, so a parameter, return or
// property typed with one refuses any other object (closure_027).

function takesClosure(Closure $c) { return "closure ok"; }
function takesNullable(?Closure $c) { return "nullable ok"; }
function takesGenerator(Generator $g) { return "generator ok"; }
function gen() { yield 1; }
function returnsClosure($v): Closure { return $v; }

class Holder { public ?Closure $c = null; }

foreach ([fn() => 1, gen(), new stdClass, new ArrayIterator([])] as $v) {
    foreach (['takesClosure', 'takesNullable', 'takesGenerator', 'returnsClosure'] as $f) {
        try {
            $r = $f($v);
            echo is_string($r) ? $r : get_class($r), "\n";
        } catch (TypeError $e) {
            echo $e->getMessage(), "\n";
        }
    }
    try {
        $h = new Holder;
        $h->c = $v;
        echo "stored\n";
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
var_dump(takesNullable(null));
