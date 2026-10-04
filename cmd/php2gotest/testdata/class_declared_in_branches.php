<?php
// A class declared in both branches of an `if` is declared by the branch
// that runs, and only that one: the other declaration is no fatal.
$n = strlen("ab");
class A
{
    public function w()
    {
        return "A";
    }
}
if ($n > 1) {
    class B extends A
    {
        public function w()
        {
            return "B extends " . parent::w();
        }
    }
} else {
    class B
    {
        public $never = 1;
    }
}
var_dump(new B, (new B)->w(), get_parent_class(new B));

// Nested branches.
if ($n < 5) {
    if ($n < 9) {
        class C
        {
            const K = "outer then inner";
        }
    } else {
        class C
        {
            const K = "inner else";
        }
    }
} else {
    class C
    {
        const K = "outer else";
    }
}
var_dump(C::K);

// A parent declared after the branch, and a class after it that extends
// the one declared.
if ($n < 5) {
    class D extends Later
    {
        public $d = 1;
    }
} else {
    class D
    {
    }
}
class Later
{
    public $l = 2;
}
class E extends B
{
}
var_dump(new D, (new E)->w());

// In a function, the declaration runs when it is called.
function declareF($n)
{
    if ($n) {
        class F
        {
            const K = "from the function";
        }
    } else {
        class F
        {
            const K = "other";
        }
    }
}
var_dump(class_exists('F'));
declareF(1);
var_dump(F::K);
