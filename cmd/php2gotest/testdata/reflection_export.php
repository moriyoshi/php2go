<?php
// The reflectors' __toString for the shapes whose whole export is known:
// user functions and their first-class callables, methods of a class with
// no parent or interfaces (trait-composed ones included), and a flat class.
// Each is the engine's _function_string / _class_string layout (probed).

function f0() {}

function f1(int $a, ?string &$b, array|int $u, Foo ...$c): ?int { return null; }

function &f2($x) { static $s = 1; return $s; }

/**
 * A multi-line
 * doc comment.
 */
function f3(): void {}

echo new ReflectionFunction('f0');
echo new ReflectionFunction('F1');
echo new ReflectionFunction('f2');
echo new ReflectionFunction('f3');
echo new ReflectionFunction(f1(...));
echo (new ReflectionFunction('f0'))->__toString();

trait T {
    final private function __construct() {}
    public function hi(int $x) {}
    /** composed doc */
    private function inner(): int { return 1; }
}

final class A {
    use T {
        hi as final;
        inner as protected exposed;
    }
}

foreach (['__construct', 'hi', 'exposed'] as $m) {
    echo new ReflectionMethod('A', $m);
}

abstract class Flat {
    public $u;
    public int $t;
    private readonly int $r;
    protected ?string $n;

    /**
     * Makes one.
     */
    function __construct(string $s) {}
    static function s(): static { return new static(""); }
    final protected function q(array $a, callable ...$rest): array { return $a; }
    private function p() {}
}

/**
 * Plain's doc.
 */
final class Plain {
    public $x;
    public int $t;
    protected ?string $n = null;
    public $d = 5;
    private readonly int $r;
    function m(): void {}
    /** m2 */
    function m2($a, $b) {}
}

echo new ReflectionMethod('Flat', 's');
echo new ReflectionMethod('Flat', 'Q');
echo new ReflectionMethod('Flat', '__construct');
echo new ReflectionClass('Plain');
echo (string) new ReflectionClass('Plain') === (new ReflectionClass('Plain'))->__toString() ? "same\n" : "differ\n";

class Q {
    public readonly int $a;
    public protected(set) readonly int $b;
    private readonly int $c;
    public private(set) int $d;
    protected readonly int $e;
    final public int $f;
}
echo new ReflectionClass('Q');
