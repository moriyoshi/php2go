<?php
// A `static` return type is the called class, resolved at run time, and the
// message names that class: `test2()` called on a B returning `new self` is
// `Return value must be of type B, A returned` (static_type_return). Unions
// and nullable forms resolve the same word; a static method resolves it by
// late static binding.

class A {
    public function viaSelf(): static { return new self; }
    public function viaStatic(): static { return new static; }
    public function union(): static|array { return new self; }
    public function nullable(): ?static { return null; }
    public static function make(): static { return new self; }
}
class B extends A {}

function show(callable $f) {
    try {
        $r = $f();
        echo $r === null ? "null" : get_class($r), "\n";
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
$a = new A;
$b = new B;
show(fn() => $a->viaSelf());
show(fn() => $b->viaSelf());
show(fn() => $b->viaStatic());
show(fn() => $b->union());
show(fn() => $b->nullable());
show(fn() => A::make());
show(fn() => B::make());

$bound = (function ($x): static { return $x; })->bindTo($a);
show(fn() => $bound($a));
show(fn() => $bound($b));

// An unbound closure has no called class, so its `static` accepts no
// object -- and the message keeps the word.
$unbound = function ($x): static { return $x; };
show(fn() => $unbound(new stdClass));
