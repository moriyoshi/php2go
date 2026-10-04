<?php
// The four lazy states present four different ways, and only one of them shows
// an `instance`. Measured across all four rather than inferred from the other
// three. `print_r` on purpose: it shows the property *set* without an object
// handle, so this pins the layout and not the numbering -- a first draft using
// var_dump failed on a handle with the layout already correct.
class R { public $a = 1; public $b; public int $t; }
class P extends R {}

$rc = new ReflectionClass(P::class);

echo "uninitialised proxy:\n";
print_r($rc->newLazyProxy(fn() => new R));
echo "\n";

echo "initialised proxy:\n";
$inProxy = $rc->newLazyProxy(fn() => new R);
$inProxy->a;
print_r($inProxy);
echo "\n";

echo "uninitialised ghost:\n";
print_r($rc->newLazyGhost(function ($o) {}));
echo "\n";

echo "initialised ghost:\n";
$inGhost = $rc->newLazyGhost(function ($o) { $o->a = 5; $o->t = 9; });
$inGhost->a;
print_r($inGhost);
echo "\n";

// An initialised proxy keeps the `lazy proxy` label var_dump gives it, where a
// ghost becomes an ordinary object.
var_dump($rc->isUninitializedLazyObject($inProxy), $rc->isUninitializedLazyObject($inGhost));
