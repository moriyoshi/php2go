<?php
// A generator that is only a temporary dies when its consumer is done with
// it, and a suspended one runs its finally blocks then -- but not those of a
// generator it delegates to that is still held elsewhere.
function gen() {
    try {
        try {
            yield 1;
        } finally {
            echo "inner finally\n";
        }
    } finally {
        echo "outer finally\n";
    }
    echo "not reached\n";
}
var_dump(gen()->current());
echo "after\n";

function source() {
    yield 42;
    yield 43;
}
function through($g) {
    yield from $g;
}
$held = source();
var_dump(through($held)->current());
var_dump(through($held)->current());
// The delegate is still alive and where it was.
$held->next();
var_dump($held->current(), $held->valid());
echo "end\n";
