<?php
// An abstract method -- an interface's, an abstract class's -- is a method
// reflection can reach and read the parameters of, though no body answers
// it: abstract methods are not compiled (object_types/type_hint_reflection).

#[Attribute]
class Marker {
    function __construct(public $v) {}
}

interface I {
    function m(object $o, #[Marker(self::class)] int &...$rest);
}

abstract class Base implements I {
    abstract protected function tpl(?string $s = null);
    static function make() {}
}

abstract class Sub extends Base {}

foreach ([["I", "m"], ["Base", "tpl"], ["Sub", "tpl"], ["Sub", "m"]] as [$class, $method]) {
    $m = (new ReflectionClass($class))->getMethod($method);
    echo "$class::$method ", $m->getName(), " ", $m->class, " ", $m->getDeclaringClass()->getName(), "\n";
    var_dump($m->getNumberOfParameters(), $m->getNumberOfRequiredParameters());
    foreach ($m->getParameters() as $p) {
        var_dump($p->getName(), (string) $p->getType(), $p->isOptional(), $p->isVariadic(), $p->isPassedByReference());
        foreach ($p->getAttributes() as $a) {
            var_dump($a->getArguments());
        }
    }
}
var_dump((new ReflectionMethod("I", "m"))->getNumberOfParameters());
try {
    (new ReflectionClass("Sub"))->getMethod("nope");
} catch (ReflectionException $e) {
    echo $e->getMessage(), "\n";
}

// An inherited concrete method reflects its declaring class too.
$make = new ReflectionMethod("Sub", "make");
echo $make->class, " ", $make->getDeclaringClass()->getName(), "\n";
