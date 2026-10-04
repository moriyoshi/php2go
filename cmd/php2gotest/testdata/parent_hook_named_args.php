<?php
// `parent::$p::set()` and `::get()` reaching a property with no parent hook
// call the implicit accessor, whose one parameter is `$value`: a named
// argument binds to it or is refused as a user function's would be, and a
// refused call's names do not leak into the next call.

class A {
    public mixed $p;
    public mixed $q {
        set(mixed $custom) { echo "A::q::set($custom)\n"; $this->q = $custom; }
    }
}

class B extends A {
    public mixed $p {
        set {
            parent::$p::set(value: $value * 10);
            try { parent::$p::set(nope: 1); } catch (Error $e) { echo $e->getMessage(), "\n"; }
        }
        get {
            try { return parent::$p::get(value: 1); } catch (Error $e) { echo $e->getMessage(), "\n"; }
            return parent::$p::get();
        }
    }
    public mixed $q {
        set {
            parent::$q::set(custom: $value);
            try { parent::$q::set(value: 2); } catch (Error $e) { echo $e->getMessage(), "\n"; }
        }
    }
}

$b = new B;
$b->p = 4;
var_dump($b->p);
$b->q = 7;
var_dump($b->q);
