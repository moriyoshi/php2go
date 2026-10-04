<?php
// A method a class takes from a trait carries the trait's attributes -- on
// the method and on its parameters -- and `self` in them is the class that
// took it. A class overriding the method has only its own. And `self::class`
// in an anonymous class's attribute is that class's own name.

#[Attribute(Attribute::TARGET_ALL)]
class A1 {
    function __construct(public $v = null) {}
}

trait T {
    #[A1(self::class)]
    function foo(#[A1(self::class)] $p) {}
    #[A1(self::class)]
    static function s() {}
}
class C { use T; }
class D {
    use T;
    #[A1("own")]
    function foo($p) {}
}
class E {
    use T;
    function foo($p) {}
}

foreach (["C", "D", "E"] as $class) {
    $m = new ReflectionMethod($class, "foo");
    echo "$class::foo: ";
    foreach ($m->getAttributes() as $a) {
        var_dump($a->getArguments());
    }
    echo count($m->getParameters()[0]->getAttributes()), "\n";
    foreach ($m->getParameters()[0]->getAttributes() as $a) {
        var_dump($a->getArguments());
    }
    var_dump((new ReflectionMethod($class, "s"))->getAttributes()[0]->getArguments());
}

$o = new #[A1(self::class)] class {
    #[A1(self::class)]
    function m() {}
};
$r = new ReflectionObject($o);
var_dump($r->getAttributes()[0]->getArguments()[0] === get_class($o));
var_dump($r->getMethod("m")->getAttributes()[0]->newInstance()->v === $r->getName());
