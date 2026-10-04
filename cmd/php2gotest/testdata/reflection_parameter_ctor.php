<?php
// new ReflectionParameter over every callable shape, its refusals, and the
// reflection of trampolines, fromCallable closures and the Closure class.

#[Attribute]
class Tag { public function __construct(public $v = null) {} }

class T {
    public function __construct(#[Tag(7)] public int $num = 0) {}
    function m($a, int $b = 1, ...$c) {}
    static function s(&$x) {}
    function __call($n, $args) { return $n; }
    static function __callStatic($n, $args) { return $n; }
}
class Plain { function only($q) {} }
function f($p, ?string $q = null) {}
$c = function (int $z) {};

function show(ReflectionParameter $r) {
    echo $r->getName(), " #", $r->getPosition(), " name=", $r->name,
        " opt=", var_export($r->isOptional(), true),
        " var=", var_export($r->isVariadic(), true),
        " ref=", var_export($r->isPassedByReference(), true), "\n";
}

echo "-- constructed\n";
show(new ReflectionParameter(['T', 'm'], 'b'));
show(new ReflectionParameter(['t', 'M'], 1));
show(new ReflectionParameter([new T, 'm'], 'c'));
show(new ReflectionParameter(['T', 's'], 0));
show(new ReflectionParameter('f', 'q'));
show(new ReflectionParameter('F', 0));
show(new ReflectionParameter($c, 'z'));
show(new ReflectionParameter(['Plain', 'only'], 'q'));

echo "-- attributes through the parameter\n";
$p = new ReflectionParameter(['T', '__construct'], 'num');
$a = $p->getAttributes()[0];
var_dump($a->getName(), $a->getArguments());
var_dump((new ReflectionProperty('T', 'num'))->getAttributes()[0]->getArguments());

echo "-- refusals\n";
$bad = [
    [['T', 'm'], 'nope'], [['T', 'm'], 5], [['T', 'm'], -1],
    ['nofunc', 'a'], ['T::s', 'x'], [['T', 'nom'], 'a'], [['NoClass', 'm'], 'a'],
    [42, 'a'], [['T'], 'a'],
];
foreach ($bad as [$fn, $prm]) {
    try {
        new ReflectionParameter($fn, $prm);
        echo "made\n";
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

echo "-- fromCallable keeps a closure\n";
$g = Closure::fromCallable($c);
var_dump($g === $c);
foreach ((new ReflectionFunction(Closure::fromCallable($c)))->getParameters() as $q) {
    echo $q->getName(), ":", $q->getType()->getName(), "\n";
}
$h = Closure::fromCallable('f');
var_dump(Closure::fromCallable($h) === $h);

echo "-- trampolines\n";
$t = new T;
foreach ([T::viaStatic(...), $t->viaInstance(...)] as $tr) {
    $rf = new ReflectionFunction($tr);
    var_dump($rf->getNumberOfParameters(), $rf->getNumberOfRequiredParameters(), $rf->hasReturnType());
    $arg = $rf->getParameters()[0];
    var_dump($arg->getName(), $arg->getPosition(), $arg->isVariadic(), $arg->isOptional(),
        $arg->isPassedByReference(), $arg->hasType(), $arg->getType()->getName());
}
// A method that exists is no trampoline.
var_dump((new ReflectionFunction($t->m(...)))->getNumberOfParameters());

echo "-- the Closure class\n";
foreach ([new ReflectionObject($c), new ReflectionClass('closure')] as $rc) {
    var_dump($rc->name, $rc->getName(), $rc->getShortName(), $rc->getNamespaceName(),
        $rc->inNamespace(), $rc->hasProperty('b'), $rc->getProperties(), $rc->getConstants(),
        $rc->isInternal(), $rc->isUserDefined(), $rc->isFinal(), $rc->isAbstract(),
        $rc->isInterface(), $rc->getParentClass(), $rc->isInstance($c), $rc->isInstance($t));
}
$bound = $c->bindTo(new stdClass);
var_dump((new ReflectionFunction($bound))->getClosureScopeClass()->name);
$bound = $c->bindTo(new stdClass, null);
var_dump((new ReflectionFunction($bound))->getClosureScopeClass()->name);
var_dump((new ReflectionFunction($c->bindTo(new T)))->getClosureScopeClass()->name);
