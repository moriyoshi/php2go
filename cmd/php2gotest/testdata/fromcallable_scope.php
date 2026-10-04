<?php
// Closure::fromCallable keeps the scope it was made in: a private method
// reached from inside the class stays callable, and a keyword callable is
// resolved -- and deprecated -- where the closure is made.
class Base {
    public function who() { return "Base::who on " . get_class($this); }
}
class Child extends Base {
    private function secret($x) { return "secret $x"; }
    public function who() { return "Child::who"; }
    public function closeSecret() { return Closure::fromCallable([$this, 'secret']); }
    public function closeParent() { return Closure::fromCallable('parent::who'); }
    public static function closeStatic() { return Closure::fromCallable([static::class, 'make']); }
    private static function make() { return "made"; }
}
$c = new Child;
echo $c->closeSecret()("kept"), "\n";
echo $c->closeParent()(), "\n";
echo Child::closeStatic()(), "\n";
try {
    $f = Closure::fromCallable([$c, 'secret']);
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
