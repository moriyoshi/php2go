<?php
// A reflector an accessor builds is a temporary: chained into a method that
// keeps no reference to it, it is freed, and the next object reuses its
// handle.
class C { public $a = 1; public int $t; public function m(int $x): int { return 1; } }
$o = new C; $r = new ReflectionClass(C::class);
$base = spl_object_id(new stdClass);
$r->getProperty("a")->skipLazyInitialization($o); echo "skip ", spl_object_id(new stdClass) - $base, "\n";
$r->getProperty("a")->setRawValueWithoutLazyInitialization($o, 5); echo "setraw ", spl_object_id(new stdClass) - $base, "\n";
$r->getProperty("a")->getValue($o); echo "getvalue ", spl_object_id(new stdClass) - $base, "\n";
$r->getProperty("a")->setValue($o, 3); echo "setvalue ", spl_object_id(new stdClass) - $base, "\n";
$r->getProperty("t")->isInitialized($o); echo "isinit ", spl_object_id(new stdClass) - $base, "\n";
$r->getProperty("a")->getName(); echo "getname ", spl_object_id(new stdClass) - $base, "\n";
$r->getMethod("m")->getName(); echo "method ", spl_object_id(new stdClass) - $base, "\n";
