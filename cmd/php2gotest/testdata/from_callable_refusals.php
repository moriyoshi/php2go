<?php
// Closure::fromCallable refuses what cannot be called, with the callback
// check's own reason, instead of building a closure that fails when called.

class A { public function method() { return "m"; } private function priv() {} public static function st() { return "s"; } }
foreach ([['A', 'method'], 'nope', [new A, 'nope'], [new A, 'priv'], 5, 'A::method'] as $c) {
    try { Closure::fromCallable($c); echo "ok\n"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
}
var_dump(Closure::fromCallable('A::st')(), Closure::fromCallable([new A, 'method'])(), Closure::fromCallable('strtoupper')("x"));
