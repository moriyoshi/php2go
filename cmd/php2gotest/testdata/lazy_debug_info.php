<?php
// `__debugInfo` is consulted **before** laziness: the engine calls it on an
// uninitialized lazy object, the initializer does not run, and the object is
// still lazy afterwards. Asking after the lazy branch made such an object dump
// its deferred slots -- exactly what the class declared `__debugInfo` to hide.
class C {
    public int $a;
    public function __construct() { $this->a = 1; }
    public function __debugInfo() { echo "debugInfo called\n"; return ['hello']; }
}
// A class with no __debugInfo still shows its deferred slots.
class P {
    public int $b;
    public string $c;
    public function __construct() { $this->b = 1; $this->c = "x"; }
}

$r = new ReflectionClass(C::class);
$ghost = $r->newLazyGhost(function ($o) { echo "ghost initializer\n"; $o->__construct(); });
echo "before ghost dump\n";
var_dump($ghost);
var_dump($r->isUninitializedLazyObject($ghost));

$proxy = $r->newLazyProxy(function ($o) { echo "proxy initializer\n"; return new C(); });
var_dump($proxy);
var_dump($r->isUninitializedLazyObject($proxy));

$q = new ReflectionClass(P::class);
$plain = $q->newLazyGhost(function ($o) { $o->__construct(); });
var_dump($plain);
var_dump($q->isUninitializedLazyObject($plain));

// And once initialized, __debugInfo still wins.
var_dump($ghost->a);
var_dump($ghost);
