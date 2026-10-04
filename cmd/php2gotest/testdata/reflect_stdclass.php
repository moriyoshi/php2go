<?php
// ReflectionClass reflects stdClass in a program that never names the class
// otherwise, and can make lazy stdClass objects that initialize on first use.

$r = new ReflectionClass('stdclass');
var_dump($r->getName());
$ghost = $r->newLazyGhost(function ($o) { echo "init\n"; $o->made = true; });
var_dump($r->isUninitializedLazyObject($ghost));
var_dump($ghost->made);
var_dump($r->isUninitializedLazyObject($ghost));
