<?php
class A {
    private function p() { echo "A::p\n"; }
    protected function q() { echo "A::q\n"; }
    public function go() { $this->p(); }
    public function reach($o) { $o->p(); }
}
class B extends A { private function p() { echo "B::p\n"; } }
class C { private function p() { echo "C::p\n"; } }
class M { private function m(){} public function __call($n, $a) { echo "__call($n)\n"; } }
class N { private static function s(){} public static function __callStatic($n,$a){ echo "__callStatic($n)\n"; } }
class S { private static function sp() { echo "S::sp\n"; } public static function ok() { static::sp(); self::sp(); } }
class D extends A { public function callq() { $this->q(); } }

$a = new A; $b = new B;
(new B)->go();
try { (new A)->reach(new C); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { $a->p(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { $b->P(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { $a->q(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
(new M)->m();
N::s();
S::ok();
try { S::sp(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
(new D)->callq();
echo "done\n";
