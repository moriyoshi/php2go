<?php
// A `callable` or `?callable` declaration refuses an int, a float or a bool
// outright -- as a return type and as a parameter -- while a callable string
// and a closure pass, and null passes only where the type is nullable.

function ret($v): ?callable { return $v; }
function param(callable $c) { return "ok"; }
foreach ([1, 2.5, true, null, 'strlen', fn() => 1] as $v) {
    try {
        var_dump(is_callable(ret($v)) || ret($v) === null);
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
foreach ([1, false, 'strlen'] as $v) {
    try {
        var_dump(param($v));
    } catch (TypeError $e) {
        echo strstr($e->getMessage(), ', called in', true), "\n";
    }
}
