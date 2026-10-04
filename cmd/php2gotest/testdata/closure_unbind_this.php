<?php
// bindTo(null) on a closure whose body reads `$this` warns and answers null;
// one that never reads it, a static one, or one made in a static method
// unbinds quietly.

class Test
{
    public function instance()
    {
        $uses = function () { return $this; };
        $not = function () { return 1; };
        $static = static function () { return 2; };
        var_dump($uses->bindTo(null), $not->bindTo(null) instanceof Closure, $static->bindTo(null) instanceof Closure);
        var_dump($uses->bindTo(new Test) instanceof Closure);
    }
    public static function fromStatic()
    {
        $uses = function () { return isset($this); };
        var_dump($uses->bindTo(null) instanceof Closure);
    }
}
(new Test)->instance();
Test::fromStatic();
