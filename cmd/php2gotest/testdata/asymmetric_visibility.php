<?php
class C {
    public private(set) int $a = 1;
    public protected(set) int $b = 2;
    protected private(set) int $c = 3;
    public function bump() { $this->a = 10; }
    public function readC() { return $this->c; }
}
class D extends C { public function tryB() { $this->b = 20; } }
$o = new C;
var_dump($o->a, $o->b);
$o->bump(); var_dump($o->a);
try { $o->a = 5; } catch (\Error $e) { echo $e->getMessage(), "\n"; }
try { $o->b = 5; } catch (\Error $e) { echo $e->getMessage(), "\n"; }
$d = new D; $d->tryB(); var_dump($d->b);
var_dump($o);
// A readonly property reports protected(set) from the engine, and its own rules
// answer for it: the message says "readonly" and refuses the SECOND write.
class R { public function __construct(public readonly int $v) {} }
$r = new R(1);
var_dump($r->v);
try { $r->v = 2; } catch (\Error $e) { echo $e->getMessage(), "\n"; }
echo "done\n";
