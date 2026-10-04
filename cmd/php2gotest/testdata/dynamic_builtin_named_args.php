<?php
// Named arguments reaching a library function through a dynamic call bind
// by the function's own parameter names, as a written call does: a required
// parameter left out is `not passed`, a name given twice overwrites, an
// unknown one is refused, and call_user_func hands names on to its callback.

function attempt(callable $f) {
    try {
        var_dump($f());
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
attempt(fn() => call_user_func('array_slice', [1, 2, 3, 4], offset: 1, length: 2));
attempt(fn() => call_user_func('array_slice', [1, 2, 3, 4], length: 2, offset: 2));
attempt(fn() => call_user_func('array_slice', [1, 2, 3], length: 2));
attempt(fn() => call_user_func('array_slice', [1, 2, 3], 1, offset: 2));
attempt(fn() => call_user_func('strlen', nope: 'x'));
attempt(fn() => call_user_func('str_repeat', times: 3, string: 'ab'));
$f = 'str_repeat';
attempt(fn() => $f(times: 2, string: 'xy'));
$show = function ($a = 'a', $b = 'b', $c = 'c') { return "$a $b $c"; };
attempt(fn() => call_user_func('call_user_func', $show, c: 'C'));
