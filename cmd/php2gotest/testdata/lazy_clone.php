<?php
// **Cloning a lazy object initialises it first.** Measured: the initializer
// runs, then `__clone`, and *both* the original and the copy are initialised
// afterwards -- the state is produced once and copied, not deferred into the
// clone. Without this the copy was made from the empty shell and the
// initializer never ran at all.
//
// No object handles are printed here on purpose: the numbering of a lazy proxy
// is a separate, still-open question, and pinning it in this fixture would make
// a numbering bug look like a clone bug.
class C {
    public int $a = 0;
    public function __construct() { $this->a = 1; }
    public function __clone() { echo "__clone ran\n"; }
}

$r = new ReflectionClass(C::class);

$ghost = $r->newLazyGhost(function ($o) { echo "ghost initializer\n"; $o->__construct(); });
echo "cloning the ghost:\n";
$gc = clone $ghost;
var_dump($r->isUninitializedLazyObject($ghost), $r->isUninitializedLazyObject($gc));
var_dump($ghost->a, $gc->a);

$proxy = $r->newLazyProxy(function () { echo "proxy initializer\n"; return new C(); });
echo "cloning the proxy:\n";
$pc = clone $proxy;
var_dump($r->isUninitializedLazyObject($proxy));
var_dump($proxy->a, $pc->a);

// The initializer runs **once**, not once per clone.
echo "cloning again:\n";
$gc2 = clone $ghost;
var_dump($gc2->a);

// An already-initialised object clones as it always did.
$plain = new C();
$plain->a = 5;
$pl = clone $plain;
var_dump($pl->a);

// An initializer that throws does so at the clone.
$bad = $r->newLazyGhost(function ($o) { throw new Exception("from initializer"); });
try {
    clone $bad;
} catch (Exception $e) {
    echo "caught: ", $e->getMessage(), "\n";
}

// **A proxy's clone keeps the proxy's class and gets its own instance.** Two
// halves of one rule, and each was broken in turn while fixing the other:
// copying the resolved instance gave the clone the *instance's* class, and
// copying the shell alone left both proxies forwarding to one object.
class Base { public string $p = "orig"; }
class Derived extends Base {}
$dr = new ReflectionClass(Derived::class);
$px = $dr->newLazyProxy(function () { return new Base(); });
$px->p;
$pxc = clone $px;
$pxc->p = "changed";
var_dump(get_class($px), get_class($pxc));
var_dump($px->p, $pxc->p);

// A ghost's clone is independent too.
$gh = $dr->newLazyGhost(function ($o) { $o->p = "ghost"; });
$gh->p;
$ghc = clone $gh;
$ghc->p = "other";
var_dump($gh->p, $ghc->p);
