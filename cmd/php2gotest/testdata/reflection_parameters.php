<?php
// getParameters() on a function, a method -- inherited or from a trait --
// and a closure answers one ReflectionParameter per declared parameter: its
// name, position, type, by-reference and variadic flags, whether it is
// optional, and its own attributes, whose `self` is the declaring class
// (attributes/013_class_scope).

#[Attribute(Attribute::TARGET_ALL | Attribute::IS_REPEATABLE)]
class A1 {
    function __construct(public $v = null) {}
}

#[Attribute(Attribute::TARGET_METHOD)]
class OnlyMethod {}

function f(#[A1(1)] int $a, &$b, ?string $c = null, #[A1(2), A1(3)] float|int ...$rest) {}

class Base {
    const K = "base";
    function m(#[A1(self::class), A1(self::K)] Base $x, #[OnlyMethod] array $y = []) {}
    static function s() {}
}
class Child extends Base {}

trait T {
    function t(#[A1(self::class)] $p) {}
}
class UsesT {
    use T;
}

function show(array $params) {
    foreach ($params as $p) {
        print_r($p);
        var_dump($p->getName(), $p->name, $p->getPosition(), $p->isOptional(),
            $p->isVariadic(), $p->isPassedByReference(), $p->canBePassedByValue(), $p->hasType());
        $t = $p->getType();
        if ($t instanceof ReflectionNamedType) {
            var_dump($t->getName(), $t->allowsNull(), (string) $t);
        } else {
            var_dump($t === null);
        }
        foreach ($p->getAttributes() as $a) {
            var_dump($a->getName(), $a->getTarget() === Attribute::TARGET_PARAMETER);
            try {
                var_dump($a->getArguments());
                var_dump($a->newInstance()->v ?? null);
            } catch (Error $e) {
                echo $e->getMessage(), "\n";
            }
        }
    }
}

$f = new ReflectionFunction('f');
var_dump($f->getNumberOfParameters(), $f->getNumberOfRequiredParameters());
show(array_slice($f->getParameters(), 0, 3));
var_dump(count($f->getParameters()[3]->getAttributes()), $f->getParameters()[3]->isVariadic(), $f->getParameters()[2]->isVariadic());
var_dump($f->getParameters()[3]->getAttributes()[1]->getArguments());

show((new ReflectionMethod('Child', 'm'))->getParameters());
var_dump((new ReflectionMethod('Base', 's'))->getParameters());
show((new ReflectionClass('UsesT'))->getMethod('t')->getParameters());

$c = #[A1] function (#[A1(self::class)] $x, $y = 2) {};
show((new ReflectionFunction($c))->getParameters());
$bound = Closure::bind($c, null, Base::class);
var_dump((new ReflectionFunction($bound))->getParameters()[0]->getAttributes()[0]->getArguments());

// `self::class` and `parent::class` in a constant expression say "Cannot
// use", and with no scope at all both name `self`; `self::X` says "Cannot
// access" (zend_ast.c).
$k = #[A1(self::class)] #[A1(self::K)] #[A1(parent::class)] function () {};
foreach ([$k, Closure::bind($k, null, Base::class), Closure::bind($k, null, Child::class)] as $each) {
    foreach ((new ReflectionFunction($each))->getAttributes() as $a) {
        try {
            var_dump($a->getArguments()[0]);
        } catch (Error $e) {
            echo $e->getMessage(), "\n";
        }
    }
}
var_dump((new ReflectionMethod("UsesT", "t"))->getNumberOfRequiredParameters(), (new ReflectionMethod("UsesT", "t"))->getNumberOfParameters());

// Two closures on one line share a name and still have their own parameters.
$one = function (int $a) {}; $two = function (#[A1(2)] string $b, $c = 1) {};
foreach ([$one, $two] as $each) {
    foreach ((new ReflectionFunction($each))->getParameters() as $p) {
        echo $p->getName(), " ", $p->getType(), " ", count($p->getAttributes()), "\n";
    }
}
