<?php
// Every conversion that reaches `__toString` reports that method's own
// failure: a comparison against a string casts the object (and an object
// without `__toString` stays greater), and a string parameter -- internal or
// userland -- passes the return-type error through instead of `X given`.

class Foo { function __toString() {} }
class Bar { function __toString() { return null; } }
class Ok { function __toString() { return "b"; } }
class Plain {}
function u(string $s) { return $s; }

$cases = [
    'cast' => fn() => (string) new Foo,
    'gt' => fn() => new Foo > '',
    'concat' => fn() => "x" . new Bar,
    'strlen' => fn() => strlen(new Foo),
    'user' => fn() => u(new Bar),
    'eq ok' => fn() => new Ok == "b",
    'lt ok' => fn() => new Ok < "c",
    'spaceship ok' => fn() => "a" <=> new Ok,
    'plain' => fn() => new Plain > "zzz",
    'plain rev' => fn() => "zzz" < new Plain,
];
foreach ($cases as $k => $f) {
    try { echo $k, ": "; var_dump($f()); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
}

// A parameter default compares through the same path.
function test($y = new Foo > '') { var_dump($y); }
try { test(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
function okd($y = new Ok == "b", $z = new Ok <=> "a") { var_dump($y, $z); }
okd();
