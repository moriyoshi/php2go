<?php
// A callable naming an ancestor's method through a class --
// `["B", "A::foo"]` -- is deprecated and runs A's method, on the caller's
// compatible `$this`; with an object, `[$this, "A::foo"]`, on that object
// (callable_self_parent_static_deprecation). It was a call to a method
// literally named `A::foo`.

class A {
    public function foo() { return static::class . " ran A::foo"; }
    public static function st() { return "static A::st"; }
}
class B extends A {
    public function test() {
        var_dump(call_user_func(["B", "A::foo"]));
        var_dump(call_user_func([$this, "A::foo"]));
        var_dump(call_user_func_array(["B", "A::st"], []));
        var_dump(array_map(["B", "A::foo"], [1]));
        var_dump(is_callable(["B", "A::foo"]));
        // A `callable` parameter's check deprecates as is_callable does:
        // the keyword forms and the class-qualified ones alike.
        foreach (["self::foo", ["parent", "foo"], ["B", "A::foo"], [$this, "parent::foo"]] as $c) {
            $this->takes($c);
        }
    }
    public function takes(callable $c) { echo "took\n"; }
}
(new B)->test();
