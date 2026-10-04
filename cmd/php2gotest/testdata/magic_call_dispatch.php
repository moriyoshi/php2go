<?php
// The name a magic method is given keeps the **case the program wrote**, and
// a static-syntax call with a compatible `$this` in scope goes to `__call` and
// not `__callStatic`. Both measured.

class A {
  public function __call($n, $a) { echo "__call: $n\n"; }
  public static function __callStatic($n, $a) { echo "__callStatic: $n\n"; }
  public function run() { $this->getFoo(); A::inStatic(); self::alsoStatic(); static::more(); }
  private function priv() { echo "priv\n"; }
}
$a = new A;
$a->getFoo();
A::fromOutside();
$a->run();
echo "private from outside: ";
$a->priv();
class B { public function __call($n, $a) { echo "B::__call $n\n"; } }
$b = new B;
$b->MiXeD();
B::alsoMixed();

class A2 {
  public function __call($n, $a) { echo "A2::__call: $n\n"; }
  public static function __callStatic($n, $a) { echo "A2::__callStatic: $n\n"; }
  public function run() { A2::inStatic(); self::alsoStatic(); static::more(); }
}
class Sub extends A2 { public function go() { A2::fromSub(); parent::fromSub2(); } }
class Other { public function ask() { A2::fromOther(); } }
$a2 = new A2;
A2::fromOutside();
$a2->run();
(new Sub)->go();
(new Other)->ask();
function plain() { A2::fromFunction(); }
plain();

// A **declared** method still resolves: folding the name for `__call` must not
// disturb the dispatch table, which is keyed by the folded name.
class Declared {
    public function __call($n, $a) { echo "should not reach __call: $n\n"; }
    public function realMethod($x = "default") { echo "realMethod($x)\n"; }
    public static function realStatic() { echo "realStatic\n"; }
}
$d = new Declared;
$d->realMethod();
$d->realMethod("given");
$d->REALMETHOD("folded");
Declared::realStatic();
