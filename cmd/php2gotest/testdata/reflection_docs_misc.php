<?php
// Reflector answers read from the declarations: doc comments (functions,
// classes, methods, properties, constants, including inherited and
// trait-composed ones), a method's final flag (declared, or given by a trait
// alias), getTraitAliases, getDefaultProperties, ReflectionParameter::
// isCallable, and ReflectionFunction::getClosure of a named function.

/** fdoc */
function documented() {}
function plain() {}

/** cdoc */
class P {
    /** pdoc */
    public $p = 1;
    private $q = 2;
    protected static $s = 3;
    public static $t;
    /** kdoc */
    const K = 1;
    const L = 2;
    /** mdoc */
    function m() {}
    function n() {}
    final function f() {}
    private $pp = 5;
}

class Q extends P {
    public $r = [1, 'two' => 2.5];
    public int $typed;
    public ?int $nullable = null;
    public $u;
    public float $widened = 3;
    public static $st = 'x';
}

trait T {
    /** tconst */
    public const TC = 42;
    /** walkdoc */
    function walk() {}
    function run() {}
    final function stop() {}
}

trait T2 {
    function run() {}
}

class R {
    use T, T2 {
        T::run insteadof T2;
        T2::run as runner;
        walk as protected stroll;
        T::run as final;
    }
}

class S extends R {}

$show = function ($v) {
    var_dump($v);
};

echo "-- doc comments\n";
$show((new ReflectionFunction('documented'))->getDocComment());
$show((new ReflectionFunction('plain'))->getDocComment());
$show((new ReflectionFunction(documented(...)))->getDocComment());
$show((new ReflectionClass('P'))->getDocComment());
$show((new ReflectionClass('Q'))->getDocComment());
$show((new ReflectionObject(new Q))->getDocComment());
$show((new ReflectionMethod('P', 'm'))->getDocComment());
$show((new ReflectionMethod('Q', 'm'))->getDocComment());
$show((new ReflectionMethod('P', 'n'))->getDocComment());
$show((new ReflectionMethod('R', 'walk'))->getDocComment());
$show((new ReflectionMethod('R', 'stroll'))->getDocComment());
$show((new ReflectionProperty('P', 'p'))->getDocComment());
$show((new ReflectionProperty('Q', 'p'))->getDocComment());
$show((new ReflectionProperty('P', 'q'))->getDocComment());
$show((new ReflectionProperty('P', 's'))->getDocComment());
$show((new ReflectionClassConstant('P', 'K'))->getDocComment());
$show((new ReflectionClassConstant('Q', 'K'))->getDocComment());
$show((new ReflectionClassConstant('P', 'L'))->getDocComment());
$show((new ReflectionClass('T'))->getReflectionConstant('TC')->getDocComment());
$show((new ReflectionClass('R'))->getReflectionConstant('TC')->getDocComment());
$dyn = new Q;
$dyn->extra = 1;
$show((new ReflectionProperty($dyn, 'extra'))->getDocComment());

echo "-- isFinal\n";
foreach ([['P', 'm'], ['P', 'f'], ['Q', 'f'], ['R', 'run'], ['R', 'walk'], ['R', 'stop'], ['R', 'runner'], ['S', 'run']] as [$c, $m]) {
    echo "$c::$m ";
    $show((new ReflectionMethod($c, $m))->isFinal());
}

echo "-- getTraitAliases\n";
$show((new ReflectionClass('R'))->getTraitAliases());
$show((new ReflectionClass('S'))->getTraitAliases());
$show((new ReflectionClass('P'))->getTraitAliases());

echo "-- getDefaultProperties\n";
$show((new ReflectionClass('P'))->getDefaultProperties());
P::$t = 99;
$show((new ReflectionClass('Q'))->getDefaultProperties());

echo "-- isCallable\n";
function typed_params(callable $x, ?callable $y, callable|int $z, $w, callable|null $u, \Closure $c) {}
foreach ((new ReflectionFunction('typed_params'))->getParameters() as $prm) {
    echo $prm->getName(), ': ';
    $show($prm->isCallable());
}

echo "-- getClosure\n";
function counter($k) {
    static $seen = [];
    $seen[] = $k;
    return count($seen);
}
$show(counter('a'));
$show((new ReflectionFunction('counter'))->getClosure()('b'));
$show(Closure::fromCallable('counter')('c'));
$show(counter('d'));
