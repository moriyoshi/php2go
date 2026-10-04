<?php
// A scalar union type is applied, and its rule is not "try the first
// alternative". Every answer here came from the engine.
class U {
    public int|string $a = 0;
    public int|float $b = 0;
    public string|float $c = "";
    public bool|int $d = 0;
    public string|bool $e = "";
    public ?int $f = null;
}
$u = new U;
foreach ([['a', 'int|string'], ['b', 'int|float'], ['c', 'string|float'], ['d', 'bool|int'], ['e', 'string|bool'], ['f', '?int']] as [$p, $ty]) {
    foreach ([1.0, 1.5, "5", "5.5", "x", true, 7, null, []] as $v) {
        echo $ty, " <- ", gettype($v), ": ";
        try {
            $u->$p = $v;
            var_dump($u->$p);
        } catch (Throwable $e) {
            echo get_class($e), ": ", $e->getMessage(), "\n";
        }
    }
}

// The same rule through a parameter and a return.
function arg(int|float $v) { var_dump($v); }
function ret(string|float $v): string|float { return $v; }
foreach (["5", "5.5", true, 7] as $v) {
    echo "arg: ";
    try { arg($v); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
    echo "ret: ";
    try { var_dump(ret($v)); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}

// A class-typed property is exact, where it used to accept any object.
class A {}
class B extends A {}
class Holder { public A $a; public stdClass $o; }
$h = new Holder;
foreach ([new B, new A, new stdClass] as $v) {
    echo "A <- ", get_class($v), ": ";
    try { $h->a = $v; echo get_class($h->a), "\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
echo "stdClass <- A: ";
try { $h->o = new A; echo get_class($h->o), "\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A float that is not an int64's worth of value is refused, not wrapped.
class N { public int $i = 0; }
$n = new N;
foreach ([1e20, -1e20, NAN, INF, "1e20", 5.5] as $v) {
    echo "int <- ", gettype($v), ": ";
    try { $n->i = $v; var_dump($n->i); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
function want_int(int $x) { var_dump($x); }
foreach ([1e20, INF, 5.5] as $v) {
    echo "arg int: ";
    try { want_int($v); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
