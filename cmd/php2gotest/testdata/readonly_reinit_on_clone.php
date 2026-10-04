<?php
// PHP 8.3 lets `__clone` write a readonly property once more -- on the object
// being cloned, and nowhere else. Three parts of the rule had to be measured:
// the scope test is the `protected(set)` one rather than the declaring-class
// one; the permission is per *object*, so another instance reached from inside
// the hook is refused normally; and `unset` reads the permission without
// spending it.
class A {
  public readonly int $p;
  public function __construct(int $p) { $this->p = $p; }
  public function __clone() {
    echo "in clone: ";
    try { $this->p = 9; echo "first ok "; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), " "; }
    try { $this->p = 10; echo "second ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
  }
}
$a = new A(1); $b = clone $a; var_dump($b->p, $a->p);
class B { public readonly int $p; public function __construct() { $this->p = 1; } }
class C extends B { public function __clone() { try { $this->p = 7; echo "sub ok\n"; } catch (Throwable $e) { echo "sub: ", get_class($e), ": ", $e->getMessage(), "\n"; } } }
$c = new C; $d = clone $c; var_dump($d->p);
class D { public readonly int $p; public function __construct() { $this->p = 1; } public function __clone() { unset($this->p); } }
$e = new D; $f = clone $e; echo "unset in clone: "; try { var_dump($f->p); } catch (Throwable $x) { echo get_class($x), ": ", $x->getMessage(), "\n"; }
class E { public readonly int $p; public function __construct() { $this->p = 1; } public function __clone() { } }
$g = new E; $h = clone $g;
echo "outside after clone: "; try { $h->p = 3; echo "ok\n"; } catch (Throwable $x) { echo get_class($x), ": ", $x->getMessage(), "\n"; }
class F { public readonly int $p; public function __construct() { $this->p = 1; } public function __clone() { $this->reinit(); } private function reinit() { try { $this->p = 4; echo "nested ok\n"; } catch (Throwable $x) { echo "nested: ", get_class($x), ": ", $x->getMessage(), "\n"; } } }
$i = new F; $j = clone $i; var_dump($j->p);
function outside($o) { try { $o->p = 77; echo "outside ok\n"; } catch (Throwable $e) { echo "outside: ", get_class($e), ": ", $e->getMessage(), "\n"; } }
class G { public readonly int $p; public function __construct() { $this->p = 1; } public function __clone() { outside($this); } }
$k = new G; $l = clone $k; var_dump($l->p);
class H { public readonly int $p; public function __construct() { $this->p = 1; } public function __clone() { $o = new H; try { $o->p = 3; echo "other obj ok\n"; } catch (Throwable $e) { echo "other obj: ", get_class($e), ": ", $e->getMessage(), "\n"; } } }
$m = new H; $n = clone $m;
class I { public readonly array $a; public function __construct() { $this->a = [1]; } public function __clone() { try { $this->a[] = 2; echo "dim ok\n"; } catch (Throwable $e) { echo "dim: ", get_class($e), ": ", $e->getMessage(), "\n"; } } }
$o = new I; $p = clone $o; var_dump($p->a);

// unset does not spend the permission: unsetting and then writing both work in
// the same hook, and so does unsetting twice.
class J {
    public readonly int $p;
    public function __construct() { $this->p = 1; }
    public function __clone() {
        unset($this->p);
        try { unset($this->p); echo "second unset ok\n"; } catch (Throwable $e) { echo "second unset: ", get_class($e), ": ", $e->getMessage(), "\n"; }
        try { $this->p = 5; echo "write after unset ok\n"; } catch (Throwable $e) { echo "write after unset: ", get_class($e), ": ", $e->getMessage(), "\n"; }
    }
}
$q = new J;
$r = clone $q;
var_dump($r->p);

// Outside a clone, unset is still refused.
class K { public readonly int $p; public function __construct() { $this->p = 1; } }
$s = new K;
echo "unset outside clone: ";
try { unset($s->p); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
