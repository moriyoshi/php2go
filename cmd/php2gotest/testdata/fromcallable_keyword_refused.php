<?php
// Closure::fromCallable('self::m') is deprecated for the keyword even when
// the method it names cannot be reached, before the TypeError.
class Foo {
    private function hidden() {}
}
class SubFoo extends Foo {
    public function get() {
        return Closure::fromCallable('self::hidden');
    }
}
try {
    (new SubFoo)->get();
} catch (TypeError $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
