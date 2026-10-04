<?php
// Two lifetime events lazy-object tests lean on. A local handed to a
// reflector method that may take it by reference (`initializeLazyObject`)
// gives its object back when it is overwritten -- the release reads the value
// the slot held, not the slot. And `ReflectionMethod::invoke` keeps no hold
// on the reflector it is called on, so a `getMethod()` temporary dies at the
// end of the statement. (`$obj` handed to invoke() as an argument is not
// claimed: the invoked method could keep it.)
class C {
    public int $a = 0;
    public function __construct() { $this->a = 1; }
    public function touch(object $o) { $o->a = 2; }
}
$reflector = new ReflectionClass(C::class);

$g = $reflector->newLazyGhost(function ($obj) { $obj->__construct(); });
$reflector->initializeLazyObject($g);
$x = new stdClass; echo "after init: ", spl_object_id($x), "\n"; unset($x);
$g = null;
$y = new stdClass; echo "after overwrite: ", spl_object_id($y), "\n"; unset($y);

for ($i = 0; $i < 2; $i++) {
    $obj = $reflector->newLazyGhost(function ($obj) { $obj->__construct(); });
    $reflector->getMethod('touch')->invoke($obj, $obj);
    var_dump($obj);
}
