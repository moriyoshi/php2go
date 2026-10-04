<?php
interface I { const IK = 9; }
trait T { public function tm() {} }
abstract class B { const BK = 2; public static $bs = "b"; public function bm() {} }
final class D extends B implements I {
    use T;
    const K = 1;
    public int $a = 1;
    public static $ds = "d";
    public function __construct() {}
    public function m() {}
    private function p() {}
    public static function s() {}
}
class Priv { private function __construct() {} }

$r = new ReflectionClass("D");
var_dump($r->getConstants());
var_dump($r->getConstant("BK"), $r->getConstant("nope"));
var_dump($r->hasConstant("BK"), $r->hasConstant("nope"));
var_dump($r->hasProperty("a"), $r->hasProperty("ds"), $r->hasProperty("nope"));
var_dump($r->hasMethod("bm"), $r->hasMethod("tm"), $r->hasMethod("NOPE"));
var_dump($r->isAbstract(), $r->isFinal(), $r->isEnum(), $r->isInstantiable());
var_dump((new ReflectionClass("B"))->isAbstract(), (new ReflectionClass("B"))->isInstantiable());
var_dump((new ReflectionClass("Priv"))->isInstantiable());
$p = $r->getParentClass();
var_dump(get_class($p), $p->getName());
var_dump((new ReflectionClass("B"))->getParentClass());
var_dump($r->getStaticProperties());
var_dump($r->getTraitNames());
var_dump($r->implementsInterface("I"), $r->isSubclassOf("B"), $r->isSubclassOf("D"), $r->isSubclassOf("I"));
var_dump(get_class($r->getConstructor()), $r->getConstructor()->getName());
var_dump((new ReflectionClass("B"))->getConstructor());
foreach ($r->getMethods() as $m) {
    echo $m->class, "::", $m->name, "\n";
}
