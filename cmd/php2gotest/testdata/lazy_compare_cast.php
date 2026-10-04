<?php
// Comparing a lazy object initializes it -- ghost or proxy, an initializer
// exception propagating out of the comparison -- while an (array) cast does
// not, and an initialized proxy casts to its instance's properties.

class C
{
    public int $a;
    public $b;
    public function __construct() { echo "C::__construct\n"; $this->a = 1; }
    public function __toString() { return 'C'; }
}
$r = new ReflectionClass(C::class);

$ghost = $r->newLazyGhost(function ($o) { $o->__construct(); });
$proxy = $r->newLazyProxy(function ($o) { return new C(); });
var_dump($ghost == $proxy);

$ghost = $r->newLazyGhost(function ($o) { $o->__construct(); });
var_dump('A' < $ghost);

$failing = $r->newLazyProxy(function () { throw new Exception('Initializer'); });
try {
    var_dump($failing > new C);
} catch (Exception $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

$proxy = $r->newLazyProxy(function ($o) { return new C(); });
var_dump((array)$proxy);
$proxy->a = 2;
var_dump((array)$proxy);
