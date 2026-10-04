<?php
// Static syntax on an engine class's instance method, from a class unrelated
// to it, cannot be called statically; a name the class lacks is undefined, and
// a real static method answers.

class test {
    public function method() {
        try { ArrayIterator::current(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
        try { ArrayObject::count(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
        try { ArrayIterator::nope(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
        var_dump(Closure::fromCallable('strlen')("abc"));
    }
}
(new test)->method();
