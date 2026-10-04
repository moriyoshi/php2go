<?php
// `new` on a constructor the calling scope cannot reach is refused before any
// argument is used, and the object dies without its destructor. The class
// itself, and for a protected one a relative, may construct.

class PrivateUser {
    private function __construct($x) { echo "ctor $x\n"; }
    public function __destruct() { echo "Destructor for ", __CLASS__, "\n"; }
    public static function make() { return new PrivateUser(1); }
}
class Prot {
    protected function __construct() { echo "prot ctor\n"; }
}
class Kid extends Prot {
    public static function make() { return new Prot; }
}
class Other {
    public static function make() { return new PrivateUser(2); }
}
function arg() { echo "arg evaluated\n"; return 3; }

try { new PrivateUser(arg()); } catch (Throwable $e) { echo $e::class, ': ', $e->getMessage(), "\n"; }
try { Other::make(); } catch (Throwable $e) { echo $e::class, ': ', $e->getMessage(), "\n"; }
try { new Prot; } catch (Throwable $e) { echo $e::class, ': ', $e->getMessage(), "\n"; }
$p = PrivateUser::make();
Kid::make();
echo "done\n";
// A protected constructor is measured against its own class: a sibling that
// shares only the parent cannot construct a class that redeclared it, and a
// subclass can construct a parent whose constructor is inherited unchanged.
class A4 { protected function __construct() { echo __METHOD__, "\n"; } }
class C4 extends A4 { protected function __construct() { echo __METHOD__, "\n"; } }
class D4 extends A4 {}
class B4 extends A4 {
    static function t() {
        try { new C4; } catch (Error $e) { echo $e->getMessage(), "\n"; }
        new D4;
        new A4;
    }
}
B4::t();
