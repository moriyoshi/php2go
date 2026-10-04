<?php
// Default parameter values. RECV_INIT carries the default in op2 and lowering
// dropped it, so `f()` threw a TypeError from the declared-type coercion where
// the engine prints the default. The default is applied **uncoerced**: the
// engine accepts `int $x = null` and prints NULL.

function plain($a = 1, $b = "two", $c = true, $d = null) {
    var_dump($a, $b, $c, $d);
}
plain();
plain(9);
plain(9, "z");
plain(9, "z", false, 3.5);

function typed(int $x = 5, string $s = "hi", float $f = 1.5, bool $t = false) {
    var_dump($x, $s, $f, $t);
}
typed();
typed(1);
// A passed argument still coerces: an int into a declared float widens.
typed(1, "a", 2, true);

function arrayDefault(array $a = [1, "k" => 2]) {
    var_dump($a);
}
arrayDefault();
arrayDefault([]);

class Holder {
    public function m(int $n = 7, $rest = "r") {
        var_dump($n, $rest);
    }
    public static function s($v = 42) {
        var_dump($v);
    }
}
$h = new Holder();
$h->m();
$h->m(1, 2);
Holder::s();
Holder::s("x");

$closure = function ($p = "closure default") {
    var_dump($p);
};
$closure();
$closure("given");
