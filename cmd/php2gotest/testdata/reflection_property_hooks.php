<?php
// PropertyHookType, the hook reflectors getHook()/getHooks() hand out, and
// the ReflectionMethod of a closure's __invoke.

#[Attribute]
class A { public function __construct(public $m = null) {} }

class P {
    #[A('prop')] public string $x = 'd' {
        #[A('pg')] get { return $this->x; }
        set (#[A('ps')] string $v) { $this->x = $v; }
    }
    public $plain;
    public $onlyGet { get => 1; }
}
class Q extends P {
    public string $x {
        #[A('qg')] get { return 'q'; }
    }
}
class R extends P {}

echo "-- the enum\n";
$s = PropertyHookType::Set;
var_dump($s, $s->name, $s->value, PropertyHookType::Get->value);
var_dump($s instanceof UnitEnum, $s instanceof BackedEnum, $s === PropertyHookType::Set);
var_dump(PropertyHookType::cases(), PropertyHookType::from('get'), PropertyHookType::tryFrom('nope'));
try { $s->value = 'x'; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
print_r($s); echo "\n";
var_export(PropertyHookType::Get); echo "\n";
echo json_encode([PropertyHookType::Get, PropertyHookType::Set]), "\n";

echo "-- hooks\n";
foreach (['P', 'Q', 'R'] as $c) {
    $p = new ReflectionProperty($c, 'x');
    $g = $p->getHook(PropertyHookType::Get);
    $h = $p->getHook(PropertyHookType::Set);
    echo $c, ": get=", $g->class, " ", $g->name, " ", $g->getName(), " ",
        $g->getAttributes()[0]->getArguments()[0], " params=", $g->getNumberOfParameters(),
        " set=", $h->class, " ", $h->name, " attrs=", count($h->getAttributes()),
        " params=", $h->getNumberOfParameters(), "/", $h->getNumberOfRequiredParameters(), " ",
        $h->getParameters()[0]->getName(), " ",
        $h->getParameters()[0]->getAttributes()[0]->getArguments()[0], " decl=",
        $g->getDeclaringClass()->getName(), "\n";
    var_dump($p->hasHooks(), $p->hasHook(PropertyHookType::Get), $p->hasHook(PropertyHookType::Set));
    var_dump(array_keys($p->getHooks()));
}
// Dumped with print_r: object numbers depend on when temporaries die.
print_r((new ReflectionProperty('P', 'x'))->getHook(PropertyHookType::Get)); echo "\n";
print_r((new ReflectionProperty('P', 'x'))->getHooks()); echo "\n";
$plain = new ReflectionProperty('P', 'plain');
var_dump($plain->getHook(PropertyHookType::Get), $plain->hasHooks(), $plain->getHooks(),
    $plain->hasHook(PropertyHookType::Set));
$only = new ReflectionProperty('P', 'onlyGet');
var_dump($only->getHook(PropertyHookType::Set), $only->hasHook(PropertyHookType::Get),
    array_keys($only->getHooks()));
foreach (['get', null, 1, new P] as $bad) {
    try {
        $only->getHook($bad);
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
try {
    $only->hasHook('set');
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
$o = new P;
$o->dyn = 1;
$dyn = new ReflectionProperty($o, 'dyn');
var_dump($dyn->hasHooks(), $dyn->getHooks(), $dyn->getHook(PropertyHookType::Get));

echo "-- a closure's __invoke\n";
class Foo {
    #[Deprecated("xyzzy")]
    public function __invoke($a = 1) { echo "In __invoke\n"; }
    public function m(int $x) {}
}
$foo = new Foo;
$closure = Closure::fromCallable($foo);
print_r($closure); echo "\n";
$test = $closure->__invoke(...);
var_dump($test === $closure);
$rm = new ReflectionMethod($test, '__invoke');
print_r($rm); echo "\n";
var_dump($rm->getName(), $rm->getAttributes()[0]->getName(), $rm->isDeprecated());
$plainRm = new ReflectionMethod($foo->m(...), '__invoke');
var_dump($plainRm->isDeprecated(), $plainRm->getAttributes());
$test();
