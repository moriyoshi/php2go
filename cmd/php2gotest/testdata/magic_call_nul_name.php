<?php
// The name `__call` and `__callStatic` receive ends at the first NUL byte:
// the engine's trampoline keeps it as a C string. A name that starts with
// NUL arrives empty (bug46238).

class A {
    static function __callStatic($name, $args) {
        var_dump($name, count($args));
    }
    function __call($name, $args) {
        var_dump($name, count($args));
    }
    function viaThis($m) {
        $this->$m(1);
        static::$m(1, 2);
    }
}

$a = 'A';
foreach (["\0", "a\0b", "\0z", "plain"] as $m) {
    $a::$m();
    (new A)->$m(1);
    (new A)->viaThis($m);
    call_user_func([new A, $m], 1);
    call_user_func(['A', $m], 1, 2);
}
