<?php
// A reference to a typed property carries that property's declaration, and the
// constraint travels with the **box** -- so every name sharing it must satisfy
// the declaration, however the write reaches it. Three sites can hold such a
// box: a variable, another property, and an array element. Only the last was
// not asking.
class C { public int $n = 1; public string $s = "a"; }
$c = new C();

// Through a variable: already worked.
$r = &$c->n;
$r .= "1";
var_dump($c->n, $r);

// Through an array element: the case that silently stored a string.
$arr = [&$c->n];
$arr[0] .= "5";
var_dump($c->n, $arr[0]);

try {
    $arr[0] .= "e50";
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
var_dump($c->n);

$arr[0] += 1;
var_dump($c->n);
$arr[0]--;
var_dump($c->n);

// A string-typed property coerces the other way.
$sarr = [&$c->s];
$sarr[0] = 13;
var_dump($c->s);

// An element that holds no box is untouched by any of this.
$plain = [1];
$plain[0] .= "9";
var_dump($plain[0]);

// Nested: an array inside an array.
$outer = [[&$c->n]];
$outer[0][0] = "42";
var_dump($c->n);
