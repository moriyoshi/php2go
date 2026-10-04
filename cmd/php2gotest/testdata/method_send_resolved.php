<?php
// A local sent to a method whose parameter mode is not known at the call
// site is settled by the method the call actually reaches.

class Target {
    public function byRef(&$x) { $x = "set by byRef"; }
    public function byVal($x) { var_dump($x); }
    public function variadicRef(&...$xs) { $xs[0] = "set by variadicRef"; $xs[1] = "second"; }
    public function variadicVal(...$xs) { var_dump($xs); }
}

class Hidden {
    private function method(&$x) { $x = "private reached"; }
    public function __call($name, $args) { echo "__call $name\n"; var_dump($args); }
    public function run() {
        $this->method($a);
        var_dump(isset($a) ? $a : "unset");
    }
}

class Outside extends Hidden {
    public function test() {
        // Hidden::method is private to Hidden: __call answers, by value.
        $this->method($u);
        var_dump(isset($u));
        $this->missing($w);
    }
}

function go(object $t) {
    $t->byRef($a);
    var_dump($a);
    $t->byVal($b);
    $t->variadicRef($c, $c2);
    var_dump($c, $c2);
    $t->variadicVal($d);
    $e = [1];
    $t->byVal($e);
    $e[] = 2;
    var_dump($e);
}

go(new Target);
(new Hidden)->run();
(new Outside)->test();
