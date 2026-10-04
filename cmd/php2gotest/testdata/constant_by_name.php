<?php
// `constant()` and `defined()` answer about a **class** constant when the name
// carries a `::`, and the answer depends on the **calling scope** -- which the
// library table cannot see, so the scope is threaded from the call site.
//
// Routing to `classConst` gets every message from one place: the private
// refusal, the trait refusal, `Undefined constant`, and the class resolved
// before the constant.
//
// `defined()` is the same lookup asked as a predicate: **false** for anything
// that would raise, including a private it cannot see.

class C
{
    private const P = 1;
    protected const R = 2;
    public const Q = 3;

    public static function inside()
    {
        return [constant('C::P'), defined('C::P'), constant('C::Q')];
    }
}

class Sub extends C
{
    public static function fromChild()
    {
        // Protected is reachable from a subclass; private is not.
        return [constant('C::R'), defined('C::P')];
    }
}

trait T
{
    public const K = 1;
}

define('PLAIN', 7);

var_dump(C::inside());
var_dump(Sub::fromChild());

foreach (['C::P', 'C::R', 'C::Q', 'C::NOPE', 'NOPE::Q', 'T::K', 'PLAIN', 'MISSING'] as $name) {
    echo $name, ' defined=';
    var_dump(defined($name));
    echo $name, ' constant=';
    try {
        var_dump(constant($name));
    } catch (\Error $e) {
        echo get_class($e), ': ', $e->getMessage(), "\n";
    }
}
