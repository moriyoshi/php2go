<?php
// ReflectionMethod::getClosure over compiled and engine methods, and the
// rules a closure made from a function or method keeps when it is rebound
// (zend_valid_closure_binding), engine classes included.

// Over a closure, getClosure() is that closure: freeing the result as a
// temporary must not free the closure's handle.
$f0 = function () { return 0; };
var_dump((new ReflectionFunction($f0))->getClosure() === $f0);
print_r((new ReflectionFunction($f0))->getClosure());
echo "\n";
var_dump(spl_object_id(new stdClass), spl_object_id($f0));

class A {
    public $n = 'a';
    function m($x = 1) { return static::class . "/" . $this->n . "/$x"; }
    static function s() { return static::class; }
    private function p() { return "p"; }
}
class B extends A {
    public $n = 'b';
    function m($x = 1) { return "B::m"; }
}
class Unrelated {}

function attempt(callable $f) {
    try {
        $f();
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
set_error_handler(function ($no, $str) {
    echo "warning: $str\n";
    return true;
});

$s = new SplStack;
$s->push(1);
$c = (new ReflectionMethod('SplStack', 'count'))->getClosure($s);
var_dump($c(), $c->bindTo(new SplQueue)());
print_r($c);
echo "\n";
$push = (new ReflectionMethod('SplQueue', 'push'))->getClosure($s);
$push(2);
var_dump(count($s));

// A's method, called on a B, runs A's body.
$m = (new ReflectionMethod('A', 'm'))->getClosure(new B);
var_dump($m(5), $m->bindTo(new A)());
$st = (new ReflectionMethod('B', 's'))->getClosure();
var_dump($st());
$p = (new ReflectionMethod('A', 'p'))->getClosure(new A);
var_dump($p());
attempt(fn() => (new ReflectionMethod('A', 'm'))->getClosure());
attempt(fn() => (new ReflectionMethod('A', 'm'))->getClosure(new Unrelated));
attempt(fn() => (new ReflectionMethod('A', 'm'))->getClosure(5));
$f = function () { return 1; };
var_dump((new ReflectionFunction($f))->getClosure() === $f);
attempt(fn() => new ReflectionMethod('Closure', '__invoke'));

$dll = (new ReflectionMethod('SplDoublyLinkedList', 'count'))->getClosure(new SplDoublyLinkedList);
foreach ([
    [new SplStack, 'SplDoublyLinkedList'],
    [new Unrelated, 'SplDoublyLinkedList'],
    [null, null],
    [new SplQueue, null],
    [new SplQueue, 'Unrelated'],
    [new SplQueue, 'SplStack'],
] as [$obj, $scope]) {
    echo $obj ? get_class($obj) : "null", ", ", $scope ?? "null", ": ";
    echo $dll->bindTo($obj, $scope) === null ? "null\n" : "bound\n";
}
$fn = (new ReflectionFunction('strlen'))->getClosure();
foreach ([[null, 'stdClass'], [null, 'A'], [new A, null], [new A, 'ArrayObject']] as [$obj, $scope]) {
    echo $obj ? get_class($obj) : "null", ", ", $scope ?? "null", ": ";
    echo $fn->bindTo($obj, $scope) === null ? "null\n" : "bound\n";
}
$static = (new ReflectionMethod('A', 's'))->getClosure();
foreach ([[new A, 'A'], [null, 'B'], [null, 'A'], [null, 'SplStack']] as [$obj, $scope]) {
    echo $obj ? get_class($obj) : "null", ", ", $scope ?? "null", ": ";
    echo $static->bindTo($obj, $scope) === null ? "null\n" : "bound\n";
}
