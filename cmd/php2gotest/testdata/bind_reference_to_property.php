<?php
// Binding a reference **to** a property -- `$o->p = &$x` -- is an indirect
// modification, and asks the same two questions reaching a place for writing
// does. This path asked neither, so a constructor could bind a reference into
// a readonly property with no refusal at all.
class A { public private(set) int $p = 1; public function set(&$x) { $this->p = &$x; } }
$a = new A; $i = 5;
echo "inside: "; try { $a->set($i); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "outside: "; try { $a->p = &$i; echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
class B { public readonly int $p; public function __construct(&$x) { $this->p = &$x; } }
echo "ro ctor: "; try { new B($i); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
class C { public int $p = 0; }
$c = new C; echo "plain: "; $c->p = &$i; var_dump($c->p); $i = 9; var_dump($c->p);
