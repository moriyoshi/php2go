<?php
// `initializeLazyObject` answers with a proxy's instance, so a program that
// keeps that answer has a second holder: the instance outlives the proxy.
class C {
    public $a = 1;
    public function __destruct() { echo "C::__destruct\n"; }
}
$r = new ReflectionClass(C::class);
$p = $r->newLazyProxy(function ($o) { return new C(); });
$inst = $r->initializeLazyObject($p);
$p = null;
echo "instance after proxy dropped: ", $inst->a, "\n";
echo "end\n";
