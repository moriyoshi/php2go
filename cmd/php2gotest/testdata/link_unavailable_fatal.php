<?php
// An override's return type naming a class not yet declared cannot be
// checked for compatibility: when the declaration runs and the class is
// still not there -- no autoloader supplies it -- that is a fatal error
// (variance/class_order_error).

echo "before\n";
class A {
    public function method(): B { return new B; }
}
class B extends A {
    public function method(): C { return new C; }
}
class C extends B {}
echo "never\n";
