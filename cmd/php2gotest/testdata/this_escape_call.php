<?php
// A method name one class declares harmlessly may be undeclared in the
// receiver's class and reach its __call, which lets $this out: once any
// __call does, every name counts as leaking (lower.MethodThisLeaks).
class Holder { static $h = []; }
class Harmless { function viaCall() { return 1; } }
class Magic { function __call($n, $a) { Holder::$h[] = $this; } }

function run() {
    $h = new Harmless;
    var_dump($h->viaCall());
    $a = new Magic;
    $a->viaCall();
    $id = spl_object_id($a);
    unset($a);
    echo "Magic ";
    var_dump(spl_object_id(new stdClass) === $id);
    var_dump(count(Holder::$h));
}
run();
