<?php
trait A {
    protected static function foo() { echo "abc\n"; }
    private static function bar() { echo "def\n"; }
    private function inst() { echo "inst\n"; }
}
class B {
    use A {
        A::foo as public;
        A::bar as public baz;
        A::inst as public reachable;
    }
}
B::foo();
B::baz();
(new B)->reachable();
try { (new B)->inst(); } catch (Error $e) { echo $e->getMessage(), "\n"; }

// A trait that composes another trait publishes the modifier onward.
trait T1 { protected function aa() { echo "T1::aa\n"; } }
trait T2 { use T1 { aa as public; } }
class P { use T1; }
class Q extends P { use T2; }
(new Q)->aa();
// The composing class decides: R uses T1 unchanged, so aa stays protected.
class R { use T1; }
try { (new R)->aa(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
echo "done\n";
