<?php
// **Initialising a lazy object releases its initializer.** The engine drops the
// closure at initialisation and its number becomes reusable at once -- but only
// when the lazy state was its *only* holder. With the same closure also kept in
// a variable the number is not reused, which is what rules out doing this
// unconditionally and forces a proof.
//
// The proof is the callers: a closure created for one call, sent by value, and
// mentioned nowhere else. It travels on the value as `SoleOwned`, the lazy
// state records it, and `runtime/php` decides *when* to release while the
// generated program supplies *how* -- the free list is the program`s.
class C { public int $a = 0; public function __construct() { $this->a = 1; } }
class P {}
$r = new ReflectionClass(C::class);

// 1. Inline closure: the lazy state is its only holder, so its number returns.
$g = $r->newLazyGhost(function ($o) { $o->__construct(); });
echo "ghost before: ", spl_object_id(new P()), "\n";
$g->a;
echo "ghost after: ", spl_object_id(new P()), "\n";

// 2. Kept in a variable: something else holds it, so the number does not.
$f = function ($o) { $o->__construct(); };
$g2 = $r->newLazyGhost($f);
echo "kept before: ", spl_object_id(new P()), "\n";
$g2->a;
echo "kept after: ", spl_object_id(new P()), "\n";
var_dump($f !== null, $g->a, $g2->a);

// 3. A proxy drops its factory the same way.
$p = $r->newLazyProxy(function () { return new C(); });
echo "proxy before: ", spl_object_id(new P()), "\n";
$p->a;
echo "proxy after: ", spl_object_id(new P()), "\n";

// 4. Never initialised: the initializer is still held.
$never = $r->newLazyGhost(function ($o) { $o->__construct(); });
echo "never: ", spl_object_id(new P()), "\n";
