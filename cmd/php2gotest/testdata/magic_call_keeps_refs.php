<?php
// `__call` receives the references it was sent only when the call was made
// the way the engine's zend_call_function makes one -- call_user_func_array,
// a callback: "don't separate references for __call" (bug50394). A direct
// call, call_user_func and an unpack send values. And the permission does not
// reach a call nested inside the callee.

function inc(&$x) { $x++; }

class Proxy {
    function __call($name, $args) {
        echo "$name: ";
        var_dump($args);
        if ($name === 'outer') {
            $this->inner(...$args);
        }
        call_user_func_array('inc', $args);
    }
    static function __callStatic($name, $args) {
        echo "static $name: ";
        var_dump($args);
        call_user_func_array('inc', $args);
    }
}

$a = 1;
$args = [&$a];
$p = new Proxy;
call_user_func_array([$p, 'bar'], $args);
call_user_func_array([$p, 'bar'], [&$a]);
var_dump($a);
call_user_func_array([$p, 'outer'], [&$a]);
var_dump($a);
call_user_func_array(['Proxy', 'st'], [&$a]);
var_dump($a);

// Values, not references, however `$a` is held.
$p->direct($a);
call_user_func([$p, 'cuf'], $a);
$p->unpacked(...$args);
var_dump($a);

function relay(&$x) {
    (new Proxy)->fromRelay($x);
}
call_user_func_array('relay', [&$a]);
var_dump($a);
