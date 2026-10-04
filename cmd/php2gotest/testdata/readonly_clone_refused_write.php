<?php
// `__clone` may write a readonly property once more, and only a write that
// lands spends that: one its type refuses leaves the permission for the
// next, which then spends it (readonly_clone_success4).

class Foo {
    public function __construct(public readonly int $bar) {}

    public function __clone() {
        foreach (["foo", 2, 3] as $v) {
            try {
                $this->bar = $v;
                echo "wrote $v\n";
            } catch (Error $e) {
                echo get_class($e), ": ", $e->getMessage(), "\n";
            }
        }
    }
}

$foo = new Foo(1);
$c = clone $foo;
var_dump($c->bar, $foo->bar);
