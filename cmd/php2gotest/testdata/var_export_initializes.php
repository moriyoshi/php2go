<?php
// `var_export` initializes a lazy object before it looks; `print_r` and
// `var_dump` deliberately do not. All three measured against the engine --
// `Materialize`'s own doc comment names var_export alongside serialize and
// json_encode, and it was the one not calling it.
class C { public int $a = 0; public function __construct() { $this->a = 7; } }
$r = new ReflectionClass(C::class);
$mk = fn() => $r->newLazyGhost(function ($o) { $o->__construct(); });

$g = $mk();
echo var_export($g, true), "\n";
var_dump($r->isUninitializedLazyObject($g));

$g2 = $mk();
echo print_r($g2, true), "\n";
var_dump($r->isUninitializedLazyObject($g2));

$g3 = $mk();
echo serialize($g3), "\n";
var_dump($r->isUninitializedLazyObject($g3));

// An ordinary object and a scalar go through var_export unchanged.
echo var_export(new C(), true), "\n";
echo var_export([1, 'two', null, true], true), "\n";
