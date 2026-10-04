<?php
class A { protected function test() {} }
class B extends A { public function test2($x) { $x->test(); echo "B ok\n"; } }
class C extends A { protected function test() {} }
class D extends C { protected function test() { echo "D::test\n"; } }
(new B)->test2(new D);

class P { protected function m(){} }
class Dd extends P { protected function m(){ echo "Dd::m\n"; } }
class Un { public function go($o){ $o->m(); } }
try { (new Un)->go(new Dd); } catch (Error $e) { echo "1: ", $e->getMessage(), "\n"; }
try { (new Dd)->m(); } catch (Error $e) { echo "2: ", $e->getMessage(), "\n"; }
trait T { protected function t(){ echo "T::t\n"; } }
class CT { use T; }
try { (new CT)->t(); } catch (Error $e) { echo "3: ", $e->getMessage(), "\n"; }
class Base { protected function b(){} }
trait T2 { protected function b(){ echo "T2::b\n"; } }
class CB extends Base { use T2; }
class Sib extends Base { public function go($o){ $o->b(); } }
(new Sib)->go(new CB);
// subclass scope calling a protected method on a parent instance
class Sub extends P { public function go2(P $o){ $o->m(); echo "Sub ok\n"; } }
(new Sub)->go2(new P);
echo "done\n";
