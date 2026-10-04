<?php
// An initializer closure the lazy state solely owns gives its number back
// whenever laziness ends without initialisation: nothing to defer at
// creation, every property skipped or raw-set, markLazyObjectAsInitialized,
// and a reset that defers nothing -- as well as on initialisation, including
// after a reset. Probed with spl_object_id of a fresh object after each step.
class C { public $a; public $b = 2; }
class E {}
$r = new ReflectionClass(C::class);
$re = new ReflectionClass(E::class);
$pa = $r->getProperty('a'); $pb = $r->getProperty('b');
function probe($l) { $x = new stdClass; echo $l, " ", spl_object_id($x), "\n"; }
probe("start");
$o1 = $re->newLazyGhost(function ($o) {});
probe("noprops");
$o2 = $r->newLazyGhost(function ($o) {});
probe("ghost made");
$pa->skipLazyInitialization($o2);
probe("skip a");
$pb->skipLazyInitialization($o2);
probe("skip all");
$o3 = $r->newLazyGhost(function ($o) {});
probe("ghost3 made");
$r->markLazyObjectAsInitialized($o3);
probe("marked");
$o4 = $r->newLazyProxy(function ($o) { return new C; });
probe("proxy made");
$pa->setRawValueWithoutLazyInitialization($o4, 1);
$pb->setRawValueWithoutLazyInitialization($o4, 1);
probe("proxy raw all");
$o5 = new C;
probe("o5");
$r->resetAsLazyGhost($o5, function ($o) {});
probe("reset");
$o5->a;
probe("reset init");
$o6 = new E;
probe("o6");
$re->resetAsLazyGhost($o6, function ($o) {});
probe("reset noprops");
$o7 = $r->newLazyGhost(function ($o) {});
probe("ghost7");
$o7->a;
probe("ghost7 init");
$o8 = $r->newLazyProxy(function ($o) { return new C; });
probe("proxy8");
$o8->a;
probe("proxy8 init");
$pa->skipLazyInitialization($o5);
probe("skip on initialized");
$o9 = $r->newLazyGhost(function ($o) {});
probe("ghost9");
$r->initializeLazyObject($o9);
probe("ghost9 initializeLazyObject");
// An initialized proxy keeps its lazy state (the instance lives there), so a
// later drop of it -- markLazyObjectAsInitialized here -- must not hand the
// initializer's number back a second time.
$o10 = $r->newLazyProxy(function ($o) { return new C; });
probe("proxy10");
$o10->a;
probe("proxy10 init");
$keep1 = new stdClass;
probe("keep1");
$r->markLazyObjectAsInitialized($o10);
probe("proxy10 marked");
$keep2 = new stdClass;
$keep3 = new stdClass;
probe("keep2 keep3");
var_dump(spl_object_id($keep1) != spl_object_id($keep2) && spl_object_id($keep2) != spl_object_id($keep3));
// markLazyObjectAsInitialized on an initialized proxy leaves it alone and
// answers with the instance.
class MC { public $a = 1; }
$r = new ReflectionClass(MC::class);
$p = $r->newLazyProxy(function () { $c = new MC; $c->a = 5; return $c; });
$p->a;
var_dump($r->markLazyObjectAsInitialized($p) === $p);
var_dump($p->a);
$p->a = 7;
var_dump($r->isUninitializedLazyObject($p), $p->a);
print_r($r->initializeLazyObject($p));
