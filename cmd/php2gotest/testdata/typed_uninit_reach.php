<?php
// Reaching an uninitialized typed property as storage: by reference a
// nullable type starts it at NULL and any other refuses; as a container for
// `[]` a type admitting an array creates one and any other refuses.
class T { public int $i; public ?int $n; public array $a; public $u; }
$t = new T;
foreach ([fn() => $r = &$t->i, fn() => $r = &$t->n, fn() => $r = &$t->a, fn() => $t->a[] = 1, fn() => $t->i[] = 1] as $k => $f) { try { $f(); echo "$k ok\n"; } catch (Error $e) { echo "$k ", $e->getMessage(), "\n"; } }
var_dump(get_object_vars($t));
