<?php
// A first-class callable whose callee is a value: `$f(...)` and
// `$o->$m(...)`. A Closure's is the Closure itself; a string or array names
// a function or method, checked when the closure is made; the method keeps
// the scope it was made in, so a private one named from inside the class
// is reachable (probed).

class O {
    function foo($x) { return "foo $x"; }
    private function p() { return "priv"; }
    function mk($n) { return $this->$n(...); }
}
$f = "strlen";
$c = $f(...);
var_dump($c("abcd"));
$cl = fn() => 1;
var_dump($cl(...) === $cl);
$o = new O;
$m = "foo";
$g = $o->$m(...);
var_dump($g(1));
var_dump(($o->mk("p"))());
$arr = [$o, "foo"];
var_dump(($arr(...))(2));
$s = "O::foo";
foreach ([fn() => ($n = "nope")(...), fn() => $o->{"bar"}(...), fn() => ($x = "bar") && $o->$x(...)] as $bad) {
    try {
        $bad();
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

// Refused when made, in the call's own words (first_class_callable_errors).
class T {
    private static function priv() {}
    public function inst() {}
    public function inside() { return self::inst(...); }
}
foreach ([
    fn() => (123)(...),
    fn() => does_not_exist(...),
    fn() => T::priv(...),
    fn() => T::inst(...),
    fn() => [new stdClass, 'nope'](...),
] as $bad) {
    try {
        $bad();
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
var_dump((new T)->inside() instanceof Closure);
