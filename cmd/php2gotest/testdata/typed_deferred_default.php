<?php
// A typed property whose default is a deferred constant expression is
// checked when the class resolves -- strictly, whatever the file's
// strict_types -- and a refusal leaves the class unresolved, so every `new`
// fails again. The one widening the language always allows still applies.

define("FOO", 5);
define("BAR", "7");
class B { public string $foo = FOO; }
class C { public int $n = BAR; }
class D { public float $f = FOO; }
for ($i = 0; $i < 2; $i++) {
    try { $o = new B(); var_dump($o->foo); } catch (Throwable $e) { echo $e->getMessage() . "\n"; }
}
try { $o = new C(); var_dump($o->n); } catch (Throwable $e) { echo $e->getMessage() . "\n"; }
$o = new D(); var_dump($o->f);
