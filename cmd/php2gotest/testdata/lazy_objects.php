<?php
// PHP 8.4 lazy objects. A lazy object has a class and a handle but **no
// state**: its properties appear the first time anything looks at one. A
// *ghost* is filled in place by its initializer.
//
// The laziness is per property, not per object, which is why the machinery
// tracks which properties have been given a value rather than a single flag.
class Point
{
    public int $a = 1;
    public string $b = 'x';
}

$r = new ReflectionClass('Point');

// The constructor is deliberately not run at creation: the declared defaults
// are what the initializer starts from, later. `made` prints before `init`.
$ghost = $r->newLazyGhost(function (Point $o) {
    echo "init\n";
    $o->a = 42;
});
echo "made\n";

// The first read is what produces the state, and the initializer runs once.
var_dump($ghost->a);
var_dump($ghost->b);
var_dump($ghost->a);

// A write reaches the state too, initializing first if nothing has yet.
$written = $r->newLazyGhost(function (Point $o) {
    echo "init2\n";
    $o->a = 7;
});
$written->b = 'set';
var_dump($written->a, $written->b);

// An initializer that hands something back is a TypeError: a ghost fills
// itself in, so there is nothing to return. The object stays lazy and the
// initializer runs again on the next access.
$bad = $r->newLazyGhost(function (Point $o) {
    return 'nope';
});
try {
    var_dump($bad->a);
} catch (Throwable $e) {
    var_dump(get_class($e), $e->getMessage());
}

// A lazy **proxy** hands off to a real instance its factory returns. The
// direction of the compatibility rule is the opposite of the obvious one: the
// proxy must be the instance's class or a subclass of it, because the proxy is
// what the program holds and type-checks against -- it may promise more than
// the real object is, never less.
//
// "No additional properties" is **counted**, which is what makes an override
// free: a subclass that redeclares an inherited property shares its entry and
// adds nothing, while one that declares a new name adds an entry.
class Base
{
    public int $n = 1;
}

class Redeclares extends Base
{
    public int $n = 2;
}

class Adds extends Base
{
    public int $extra = 3;
}

$proxy = (new ReflectionClass('Redeclares'))->newLazyProxy(function (Redeclares $o) {
    return new Base();
});
var_dump($proxy->n);

$bad = (new ReflectionClass('Adds'))->newLazyProxy(function (Adds $o) {
    return new Base();
});
try {
    var_dump($bad->n);
} catch (Throwable $e) {
    var_dump(get_class($e), $e->getMessage());
}

// Forcing and inspecting the state. `initializeLazyObject` answers the object
// holding the state, which for a proxy is the instance rather than the proxy.
$forced = (new ReflectionClass('Point'))->newLazyGhost(function (Point $o) {
    echo "forced\n";
    $o->a = 9;
});
$rp = new ReflectionClass('Point');
var_dump($rp->isUninitializedLazyObject($forced));
$rp->initializeLazyObject($forced);
var_dump($rp->isUninitializedLazyObject($forced), $forced->a);

// The two raw writers take a property out of the deferred set without running
// anything. **Laziness is per property**: once the last one has a value the
// object is not lazy at all and the initializer never runs -- which is why the
// second write below makes `isUninitializedLazyObject` false with nothing
// printed.
$raw = $rp->newLazyGhost(function (Point $o) {
    echo "never runs\n";
});
$pa = new ReflectionProperty('Point', 'a');
$pb = new ReflectionProperty('Point', 'b');
$pa->setRawValueWithoutLazyInitialization($raw, 77);
var_dump($rp->isUninitializedLazyObject($raw));
$pb->skipLazyInitialization($raw);
var_dump($rp->isUninitializedLazyObject($raw), $raw->a, $raw->b);

// skipLazyInitialization on a property that is already not lazy does nothing:
// it must not write the declared default a second time over what the program
// has since put there. A raw write does overwrite, having been given a value.
$pb->skipLazyInitialization($raw);
var_dump($raw->b);

// **A raw write is a write.** `setRawValueWithoutLazyInitialization` carries
// the property's declared type, so it coerces or refuses exactly as an
// assignment would, and on an already-initialized readonly property it is
// `Cannot modify readonly property`. `skipLazyInitialization` writes nothing of
// the caller's and is refused for neither reason.
class Raw
{
    public int $n = 0;
    public readonly string $r;

    public function __construct()
    {
        $this->r = 'set';
    }
}

$rawTarget = new Raw();
try {
    (new ReflectionProperty('Raw', 'n'))->setRawValueWithoutLazyInitialization($rawTarget, new stdClass());
} catch (Throwable $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    (new ReflectionProperty('Raw', 'r'))->setRawValueWithoutLazyInitialization($rawTarget, 'again');
} catch (Throwable $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
(new ReflectionProperty('Raw', 'r'))->skipLazyInitialization($rawTarget);
echo "skip ok\n";
(new ReflectionProperty('Raw', 'n'))->setRawValueWithoutLazyInitialization($rawTarget, '42');
var_dump($rawTarget->n);
