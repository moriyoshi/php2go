<?php
// getIterator() must return something traversable: an array, a scalar or a
// plain object is an Exception naming the aggregate; an iterator or a
// generator is iterated.

class A implements IteratorAggregate { function getIterator(): Traversable { return new ArrayIterator([1, 2]); } }
class B implements IteratorAggregate { #[ReturnTypeWillChange] function getIterator() { return ['foo']; } }
class C implements IteratorAggregate { #[ReturnTypeWillChange] function getIterator() { return new stdClass; } }
class G implements IteratorAggregate { function getIterator(): Generator { yield 7; } }
foreach ([new A, new B, new C, new G] as $o) {
    try { foreach ($o as $v) { var_dump($v); } } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
