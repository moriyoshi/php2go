<?php
// The engine's exception constructors take their arguments as an internal
// function does: coerced in weak mode, refused with a TypeError naming the
// class that declares the constructor.
$e = new Exception(5, "7");
var_dump($e->getMessage(), $e->getCode());
class Mine extends RuntimeException {}
$m = new Mine("m", 3, $e);
var_dump(get_class($m->getPrevious()), $m->getCode());
foreach ([
    fn() => new Exception(new stdClass),
    fn() => new Exception("m", "x"),
    fn() => new Exception("m", 1, "not a throwable"),
    fn() => new Error([], 1),
    fn() => new Mine(new stdClass),
] as $make) {
    try {
        $make();
        echo "made\n";
    } catch (TypeError $t) {
        echo $t->getMessage(), "\n";
    }
}
$n = new Exception(null);
var_dump($n->getMessage());
