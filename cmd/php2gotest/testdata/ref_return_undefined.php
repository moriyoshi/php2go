<?php
// A by-reference return of an undefined variable creates it as null: the
// engine fetches the returned variable for writing. So `$b = f()` is null
// with no warning, and a by-reference `key()` returning one gives the
// foreach key null (iterator_key_by_ref).

function &f() { return $x; }
$a = &f();
var_dump($a);
$b = f();
var_dump($b);

function &g($flag) {
    if ($flag) {
        $y = "set";
    }
    return $y;
}
var_dump(g(false), g(true));

class C {
    static function &h() { return $u; }
}
$e = C::h();
var_dump($e);

class Test extends ArrayIterator {
    #[ReturnTypeWillChange]
    function &key() {
        return $foo;
    }
}
foreach (new Test([0, 1]) as $k => $v) {
    var_dump($k, $v);
}
