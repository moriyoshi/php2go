<?php
// debug_backtrace() hands any callee its caller's object by default, so a
// method that never names $this can still let it out (lower.ReachesCallerThis):
// once a program mentions it, no receiver is claimed by the summary.
class Holder { static $h = []; }

function grab() {
    $frames = debug_backtrace();
    Holder::$h[] = $frames[1]['object'];
}

class Quiet {
    public $n = 1;
    function go() { grab(); return $this->n; }
}

function run() {
    $a = new Quiet;
    var_dump($a->go());
    $id = spl_object_id($a);
    unset($a);
    echo "Quiet ";
    var_dump(spl_object_id(new stdClass) === $id);
    var_dump(Holder::$h[0] instanceof Quiet);
}
run();
