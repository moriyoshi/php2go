<?php
// `$f()` refuses what cannot be called in the engine's words, each shape its
// own, as a catchable Error; an object is callable only through __invoke.
class Inv { function __invoke() { echo "inv\n"; } }
foreach ([["foo"], [1, 2, 3], [1, "m"], [new stdClass, 1], ["stdClass", 5], [], new stdClass, 5, null, 1.5, true, new Inv] as $f) {
    try { $f(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
class Magic { function __call($n, $a) { echo "call\n"; } }
try { $m = new Magic; $m(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
