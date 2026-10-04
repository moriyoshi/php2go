<?php
// A constructor that keeps `$this` -- in a global, or in the object itself,
// a cycle only the collector frees -- keeps the object past its frame: it is
// not destroyed as the function returns. A trait's constructor counts.
class G {
    public function __construct(public $n) { $GLOBALS['kept'][] = $this; }
    public function __destruct() { echo "G{$this->n} destroyed\n"; }
}
class C {
    public $self;
    public function __construct(public $n) { $this->self = $this; }
    public function __destruct() { echo "C{$this->n} destroyed\n"; }
}
class Plain {
    public function __construct(public $n) {}
    public function __destruct() { echo "Plain{$this->n} destroyed\n"; }
}
trait KeepsThis {
    public $self;
    public function __construct(public $n) { $this->self = $this; }
}
class T {
    use KeepsThis;
    public function __destruct() { echo "T{$this->n} destroyed\n"; }
}
function f() {
    new G(1);
    $x = new G(2);
    $p = new Plain(1);
    $t = new T(1);
    echo "end f\n";
}
f();
echo "after f\n";
function g() {
    new C(1);
    $y = new C(2);
    echo "end g\n";
}
g();
echo "after g\n";
echo "end of script\n";
