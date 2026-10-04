<?php
// A static persists across calls.
function counter() { static $n = 0; $n++; return $n; }
var_dump(counter(), counter(), counter());
// A method's static is shared by every INSTANCE...
class C { public function m() { static $k = 10; $k++; return $k; } }
$a = new C; $b = new C;
var_dump($a->m(), $b->m(), $a->m());
// ...and by a subclass that inherits the method: one slot per definition.
class D extends C {}
var_dump((new D)->m());
// The initializer runs on first entry only.
function once() { static $s = "init"; $r = $s; $s = "later"; return $r; }
var_dump(once(), once());
// A closure with both a `use` capture and a static: they share one opcode and
// are told apart by whether the table gives an initializer.
$cap = 5;
$f = function () use ($cap) { static $c = 100; $c++; return [$cap, $c]; };
var_dump($f(), $f());
echo "done\n";
