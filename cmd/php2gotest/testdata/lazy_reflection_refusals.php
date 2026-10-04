<?php
// The lazy-object reflection API's refusals, in the engine's order, on the
// compiled path: the options mask (invalid bits, then SKIP_DESTRUCTOR on the
// newLazy* forms), resetAsLazy*'s type, its "being initialized" and "already
// lazy" states, the reset's destructor (and SKIP_DESTRUCTOR), and the three
// property kinds skipLazyInitialization / setRawValueWithoutLazyInitialization
// refuse. Also: a parent's private property is not the subclass's to reflect.

class C { public $a = 1; public $n = 0; }
class Sub extends C {}
class Other {}
$r = new ReflectionClass(C::class);

foreach ([0, 8, 16, 24, 32, 1, -1] as $f) {
    foreach (['newLazyGhost', 'newLazyProxy'] as $m) {
        try {
            $r->$m(function () {}, $f);
            echo "$m $f ok\n";
        } catch (ReflectionException $e) {
            echo "$m $f ", $e->getMessage(), "\n";
        }
    }
    foreach (['resetAsLazyGhost', 'resetAsLazyProxy'] as $m) {
        $o = new C;
        try {
            $r->$m($o, function () {}, $f);
            echo "$m $f ok\n";
        } catch (ReflectionException $e) {
            echo "$m $f ", $e->getMessage(), "\n";
        }
    }
}

// The reflected class is the parameter's type: a subclass will do.
$r->resetAsLazyGhost(new Sub, function () {});
echo "subclass ok\n";
try {
    $r->resetAsLazyGhost(new Other, function () {});
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}

// Already lazy; invalid flags are refused before the state is looked at.
$o = $r->newLazyGhost(function () {});
foreach ([0, -1] as $f) {
    try {
        $r->resetAsLazyProxy($o, function () {}, $f);
    } catch (ReflectionException $e) {
        echo $e->getMessage(), "\n";
    }
}
// An initialized ghost or proxy may be reset.
$o->a;
$r->resetAsLazyGhost($o, function () {});
echo "initialized ghost reset\n";
$p = $r->newLazyProxy(function () { return new C; });
$p->a;
$r->resetAsLazyGhost($p, function () {});
echo "initialized proxy reset\n";

// From inside the initializer.
$o = new C;
$r->resetAsLazyGhost($o, function ($o) use ($r) {
    foreach (['resetAsLazyGhost', 'resetAsLazyProxy'] as $m) {
        try {
            $r->$m($o, function () {});
        } catch (Error $e) {
            echo get_class($e), ": ", $e->getMessage(), "\n";
        }
    }
});
$r->initializeLazyObject($o);

// The reset runs the destructor unless told not to; a throw is the reset's
// and the object stays as it was.
class D {
    public $a = 1;
    public static $throw = false;
    function __destruct() {
        echo "D::__destruct\n";
        if (self::$throw) {
            throw new Exception("from dtor");
        }
    }
}
$rd = new ReflectionClass(D::class);
$d = new D;
echo "reset\n";
$rd->resetAsLazyGhost($d, function ($d) { echo "init D\n"; });
echo "after reset\n";
$d->a;
$d2 = new D;
echo "reset skipping\n";
$rd->resetAsLazyGhost($d2, function () {}, ReflectionClass::SKIP_DESTRUCTOR);
echo "after reset skipping\n";
D::$throw = true;
$d3 = new D;
try {
    $rd->resetAsLazyGhost($d3, function () {});
} catch (Exception $e) {
    echo "caught ", $e->getMessage(), "\n";
}
var_dump($rd->isUninitializedLazyObject($d3));
D::$throw = false;

// SKIP_INITIALIZATION_ON_SERIALIZE is recorded, not refused.
$s = $r->newLazyGhost(function () { echo "init for serialize\n"; }, ReflectionClass::SKIP_INITIALIZATION_ON_SERIALIZE);
echo serialize($s), "\n";
$s = $r->newLazyGhost(function ($o) { echo "init for serialize\n"; });
echo serialize($s), "\n";

// Three property kinds defer nothing and are refused, lazy object or not.
#[AllowDynamicProperties]
class Dyn { public $a = 1; }
$dyn = new Dyn;
$dyn->dyn = 1;
$pd = new ReflectionProperty($dyn, 'dyn');
try {
    new ReflectionProperty($dyn, 'nope');
} catch (ReflectionException $e) {
    echo $e->getMessage(), "\n";
}
$lazyDyn = (new ReflectionClass(Dyn::class))->newLazyGhost(function () {});
foreach ([$lazyDyn, $dyn] as $target) {
    try {
        $pd->skipLazyInitialization($target);
    } catch (ReflectionException $e) {
        echo $e->getMessage(), "\n";
    }
    try {
        $pd->setRawValueWithoutLazyInitialization($target, 5);
    } catch (ReflectionException $e) {
        echo $e->getMessage(), "\n";
    }
}
class S { public static $s; public int $v { get => 1; } public $plain; }
$os = (new ReflectionClass(S::class))->newLazyGhost(function () {});
foreach (['s', 'v'] as $n) {
    $pp = new ReflectionProperty(S::class, $n);
    try {
        $pp->skipLazyInitialization($os);
    } catch (ReflectionException $e) {
        echo $e->getMessage(), "\n";
    }
    try {
        $pp->setRawValueWithoutLazyInitialization($os, 5);
    } catch (ReflectionException $e) {
        echo $e->getMessage(), "\n";
    }
}

// A parent's private property, instance or static, is not the subclass's.
class PB { private $b = 1; protected $p = 2; private static $sb; }
class PC extends PB { public $a; private $b = 'own'; }
class PD extends PB { public $x; }
$pc = new PC;
foreach ([PD::class, new PD] as $subject) {
    foreach (['b', 'p', 'sb'] as $n) {
        try {
            $rp = new ReflectionProperty($subject, $n);
            echo "made ", $rp->name, "\n";
        } catch (ReflectionException $e) {
            echo $e->getMessage(), "\n";
        }
    }
}
$rpd = new ReflectionClass(PD::class);
var_dump($rpd->hasProperty('b'), $rpd->hasProperty('p'), $rpd->hasProperty('sb'));
try {
    $rpd->getProperty('b');
} catch (ReflectionException $e) {
    echo $e->getMessage(), "\n";
}
// A class's own private property of the same name as its parent's is its own.
$rpc = new ReflectionClass(PC::class);
var_dump($rpc->hasProperty('b'), $rpc->getProperty('b')->getValue($pc));
foreach ($rpc->getProperties() as $prop) {
    echo $prop->name, "\n";
}
