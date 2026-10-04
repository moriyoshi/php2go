<?php
// `new self` and `new parent` in a default value resolve when the default is
// used, against the declaring class: with no class, or no parent, that is an
// Error at the call -- and nothing at all when an argument is passed.

class A
{
    public static function noParent($x = new parent) { return get_class($x); }
}
class B extends A
{
    public static function both($x = new self, $y = new parent) { return get_class($x) . "/" . get_class($y); }
}
function free($x = new self) { return "passed " . get_class($x); }

var_dump(B::both());
var_dump(free(new B), A::noParent(new A));
foreach (['free', [A::class, 'noParent']] as $f) {
    try {
        $f();
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
