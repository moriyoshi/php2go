<?php
// `f(...)` makes a Closure of a call the engine never performs.
function plain($x) { return $x + 1; }
$f = plain(...);
var_dump($f(1));
class C {
    public $p = 5;
    public function m($x) { return $x + $this->p; }
    public static function s($x) { return $x * 2; }
}
$o = new C;
// The instance form carries the receiver.
$g = $o->m(...);
var_dump($g(1));
$o->p = 100;
var_dump($g(1));
// The static form does not.
$h = C::s(...);
var_dump($h(3));
// A builtin works the same way.
$k = count(...);
var_dump($k([1, 2, 3]));
// The result is an ordinary Closure: it answers the Closure surface.
var_dump($h->__invoke(4));
echo "done\n";
// `$this->m(...)` inside a method names the receiver, which is a parameter and
// not a local slot.
class Inner {
    private function hidden() { return "hidden"; }
    public function expose() { return $this->hidden(...); }
}
$e = (new Inner)->expose();
var_dump($e());
