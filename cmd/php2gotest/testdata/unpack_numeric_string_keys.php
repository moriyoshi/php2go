<?php
// Unpacking a Traversable whose keys are numeric strings passes them as
// positional arguments; only a non-numeric string names a parameter.
function g() {
    yield '100' => 'first';
    yield '101' => 'second';
    yield '0102' => 'named by a string that is not canonical';
}
function h() {
    yield '7' => 'a';
    yield 'y' => 'b';
}
function test($x = null, $y = null, ...$z) {
    var_dump($x, $y, $z);
}
test(...g());
test(...h());
