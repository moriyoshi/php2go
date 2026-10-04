<?php
// Class references written as expressions or keywords: `C::{expr}` names a
// constant at run time -- a non-string is a TypeError, `{'class'}` is the
// class's name, and the class is fetched first (dynamic_class_const_fetch_*,
// gh10486); `instanceof` takes an expression class, a string or an object,
// and `self`/`parent`/`static`, which in a closure outside any class are
// asked when it runs (new_instanceof_expr, self_instanceof_outside_class).

class P { const X = 'px'; }
class C extends P {
    const X = 'cx';
    function names($w) {
        return [static::{$w}, parent::{$w}, self::{$w}];
    }
    function checks($o) {
        return [$o instanceof self, $o instanceof parent, $o instanceof static];
    }
}
function attempt(callable $f) {
    try {
        var_dump($f());
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
$n = 'X';
attempt(fn() => C::{$n});
attempt(fn() => C::{'class'});
attempt(fn() => C::{1});
attempt(fn() => Missing::{1});
attempt(fn() => C::{strtolower('X') . ''});

$class = 'class';
$obj = new stdClass;
attempt(fn() => $obj instanceof ('std' . $class));
attempt(fn() => new C instanceof ('P'));
attempt(fn() => new C instanceof (new P));
attempt(fn() => new C instanceof ('std' . $class));
var_dump((new C)->checks(new C), (new C)->checks(new P));
var_dump((new C)->names('class'), (new C)->names('X'));
$fn = function () {
    try {
        return new stdClass instanceof self;
    } catch (Error $e) {
        return $e->getMessage();
    }
};
var_dump($fn());
var_dump(Closure::bind($fn, null, C::class)());
