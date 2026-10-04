<?php
// markLazyObjectAsInitialized() leaves an object as a fresh one would be:
// every deferred property takes its declared default, a value set without
// initialization is kept, and no initializer runs.

class C { public int $a = 1; public int $b = 2; public $c; public int $typedNoDefault; }
$r = new ReflectionClass(C::class);
foreach (['ghost' => fn() => $r->newLazyGhost(function () { echo "never\n"; }),
          'proxy' => fn() => $r->newLazyProxy(function () { echo "never\n"; return new C; })] as $kind => $make) {
    $obj = $make();
    $r->getProperty('b')->setRawValueWithoutLazyInitialization($obj, 20);
    var_dump($r->markLazyObjectAsInitialized($obj) === $obj);
    var_dump($r->isUninitializedLazyObject($obj));
    echo "$kind: ", json_encode(get_object_vars($obj)), "\n";
}
