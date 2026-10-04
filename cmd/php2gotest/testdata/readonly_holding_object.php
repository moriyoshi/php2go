<?php
// A readonly property holding an object: writes through the object are
// fine, `++`/`--`/compound assignment on the property are refused before
// anything is computed, and a reference taken to it is to a copy of the
// handle, so assigning through the reference leaves the property alone.

class Test
{
    public readonly object $prop;
    public function __construct(object $prop) { $this->prop = $prop; }
}
$test = new Test(new stdClass);
$test->prop->foo = 1;
$test->prop->foo += 1;
$test->prop->foo++;
foreach ([fn() => $test->prop += 1, fn() => $test->prop++, fn() => --$test->prop] as $f) {
    try {
        $f();
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
var_dump($test->prop->foo);
$ref =& $test->prop;
$ref = new ArrayObject();
var_dump(get_class($test->prop), get_class($ref));

// `__clone` may still step a readonly property once.
class Counter
{
    public function __construct(public readonly int $n) {}
    public function __clone() { $this->n++; }
}
$c = clone new Counter(1);
var_dump($c->n, (clone $c)->n);
