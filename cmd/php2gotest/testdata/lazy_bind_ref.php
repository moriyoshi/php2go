<?php
// Binding a reference to a lazy object's property initializes it first; on a
// proxy the binding lands on the instance, whose typed property then refuses
// the wrong value.

class Test { public int $prop = 0; public $free; }
$r = new ReflectionClass(Test::class);

$proxy = $r->newLazyProxy(function () { echo "init proxy\n"; return new Test(); });
$ref = "foobar";
try {
    $proxy->prop =& $ref;
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump($r->isUninitializedLazyObject($proxy));

$ghost = $r->newLazyGhost(function ($o) { echo "init ghost\n"; });
$x = 5;
$ghost->free =& $x;
$x = 6;
var_dump($ghost->free, $r->isUninitializedLazyObject($ghost));

// The proxy's binding is the instance's: a later write through the reference
// is what reading through the proxy sees.
$proxy = $r->newLazyProxy(function () { return new Test(); });
$y = 1;
$proxy->free =& $y;
$y = 7;
var_dump($proxy->free);
