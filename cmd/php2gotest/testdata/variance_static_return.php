<?php
// A parent method returning `static` binds an override declared at run time
// to `static` too: a class name is never compatible, whatever is loaded.
class A {
    public function same(): static { return $this; }
    public function other(): static { return $this; }
}
spl_autoload_register(function ($class) {
    echo "load $class\n";
    class B extends A {
        public function same(): static { return $this; }
        public function other(): A { return $this; }
    }
});
new B;
echo "not reached\n";
