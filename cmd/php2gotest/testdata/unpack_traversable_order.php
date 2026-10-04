<?php
// Unpacking a Traversable into arguments: a string key names an argument,
// and an integer key after one is refused as for an array.

function show(...$args) { var_dump($args); }
show(...new ArrayIterator([1, 2, "foo" => 3]));
try {
    show(...new ArrayIterator([1, 2, "foo" => 3, 4]));
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
function gen() { yield "a" => 1; yield 2; }
try {
    show(...gen());
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
