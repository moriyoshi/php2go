<?php
// A default the compiler kept as a constant expression is exported as
// zend_ast_export writes it: the constant by its compiled name, a class
// constant and a `new` with the class as it was qualified.
namespace N {
    const Q = 3;
    class K
    {
        const C = 2;
        public function __construct(...$a) {}
        public function m($a = self::C, $b = new self, $c = -INF, $d = \PHP_EOL) {}
    }
    function f($a = INF, $b = K::C, $c = new K, $d = -INF, $e = NAN, $f = PHP_INT_MAX, $g = \E_ALL, $h = Q, $k = new K(1, 'x', -2, Q), $m = \M_PI, $p = K::class) {}
    echo new \ReflectionFunction('N\f');
    echo new \ReflectionMethod('N\K', 'm');
}
namespace {
    class G
    {
        const C = 1;
    }
    function g($a = INF, $b = G::C, $c = \G::C, $d = new G, $e = new \G, $f = -INF, $g = \INF, $h = PHP_EOL) {}
    echo new ReflectionFunction('g');
}
