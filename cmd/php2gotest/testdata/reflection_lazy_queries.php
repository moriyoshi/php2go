<?php
// Lazy-object questions a reflector answers without initializing:
// ReflectionClass::getLazyInitializer (the callable as given, null once the
// object is no longer lazy), ReflectionProperty::isLazy (through
// initialized proxies, false for static, virtual and dynamic properties),
// and ReflectionObject::__toString of an uninitialized lazy object.

#[AllowDynamicProperties]
class C {
    public static $staticProp;
    public int $typed;
    public $untyped = 'u';
    public $virtual {
        get { return 1; }
    }
}

class D {}

function init_ghost($o) {
    echo "init ghost\n";
}

$r = new ReflectionClass(C::class);

echo "-- getLazyInitializer\n";
$byName = 'init_ghost';
$closure = function ($o) {
    echo "closure init\n";
};
$array = [new class {
    function go($o) {
        echo "array init\n";
    }
}, 'go'];
foreach ([$byName, $closure, $array] as $i => $init) {
    $o = $r->newLazyGhost($init);
    var_dump($r->getLazyInitializer($o) === $init);
    $r->initializeLazyObject($o);
    var_dump($r->getLazyInitializer($o));
}
var_dump($r->getLazyInitializer(new C));
try {
    $r->getLazyInitializer(new D);
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}

echo "-- isLazy\n";
function props(ReflectionClass $r, object $o) {
    foreach (['staticProp', 'typed', 'untyped', 'virtual'] as $name) {
        printf("%s: %d\n", $name, $r->getProperty($name)->isLazy($o));
    }
    $tmp = new C();
    $tmp->dynamic = 1;
    printf("dynamic: %d\n", (new ReflectionProperty($tmp, 'dynamic'))->isLazy($o));
}

$ghost = $r->newLazyGhost(function ($o) {
});
props($r, $ghost);
$r->getProperty('typed')->skipLazyInitialization($ghost);
printf("typed skipped: %d\n", $r->getProperty('typed')->isLazy($ghost));
$r->initializeLazyObject($ghost);
props($r, $ghost);

$inner = new C();
$proxy = $r->newLazyProxy(function () use ($inner) {
    return $inner;
});
props($r, $proxy);
$r->initializeLazyObject($proxy);
props($r, $proxy);
$r->resetAsLazyProxy($inner, function () {
    return new C();
});
echo "nested:\n";
props($r, $proxy);
try {
    $r->getProperty('typed')->isLazy(new D);
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}

echo "-- ReflectionObject::__toString\n";
$fresh = $r->newLazyGhost(function ($o) {
    echo "should not run\n";
});
$text = (new ReflectionObject($fresh))->__toString();
echo substr_count($text, "Dynamic properties [0]"), "\n";
var_dump($r->isUninitializedLazyObject($fresh));
