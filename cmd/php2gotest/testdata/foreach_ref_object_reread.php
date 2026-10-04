<?php
// A by-reference foreach over a variable holding a plain object re-reads the
// variable at every step, as it does for an array (gh20085): the same object
// goes on, any other object or an array is walked from its first element, a
// scalar warns and ends the loop, and fetching a lazy object's properties
// initialises it -- an initializer that throws does so at the foreach line.

class P { public $x = 1; public $y = 2; public $z = 3; }
class Q { public $u = 10; public $v = 20; public $w = 30; }

// Another object, from the next step.
$o = new P; $n = 0;
foreach ($o as $k => &$v) { echo "other: $k\n"; if (++$n == 1) $o = new Q; if ($n > 8) { echo "LOOP\n"; break; } }

// A fresh object of the same class restarts.
$o = new P; $n = 0;
foreach ($o as $k => &$v) { echo "same class: $k\n"; if (++$n == 2) $o = new P; if ($n > 8) { echo "LOOP\n"; break; } }

// The same object handed back goes on.
$o = new P; $keep = $o; $n = 0;
foreach ($o as $k => &$v) { echo "same: $k\n"; if (++$n == 1) $o = $keep; if ($n > 8) { echo "LOOP\n"; break; } }

// Object to array, array to object.
$o = new P; $n = 0;
foreach ($o as $k => &$v) { echo "to array: $k\n"; if (++$n == 1) $o = [7, 8, 9]; if ($n > 8) { echo "LOOP\n"; break; } }
$o = [1, 2, 3]; $n = 0;
foreach ($o as $k => &$v) { echo "to object: $k\n"; if (++$n == 1) $o = new Q; if ($n > 8) { echo "LOOP\n"; break; } }

// A scalar ends the loop with the foreach's warning.
$o = new P; $n = 0;
foreach ($o as $k => &$v) { echo "scalar: $k\n"; if (++$n == 1) $o = 5; if ($n > 8) { echo "LOOP\n"; break; } }

// Writes through the binding reach the object walked at the time.
$a = new P; $b = new Q; $o = $a; $n = 0;
foreach ($o as $k => &$v) { $v = "w$k"; if (++$n == 1) $o = $b; if ($n > 8) break; }
unset($v);
var_dump(get_object_vars($a), get_object_vars($b));

// Handed a Traversable, the loop walks its property table like any other
// object's: an ArrayIterator and a generator have none, an
// IteratorAggregate's public properties are walked.
$o = new P; $n = 0;
foreach ($o as $k => &$v) { echo "to iterator: $k\n"; if (++$n == 1) $o = new ArrayIterator(['a' => 1, 'b' => 2]); if ($n > 8) { echo "LOOP\n"; break; } }
function gen() { yield 1; yield 2; }
$o = new P; $n = 0;
foreach ($o as $k => &$v) { echo "to generator: $k\n"; if (++$n == 1) $o = gen(); if ($n > 8) { echo "LOOP\n"; break; } }
class Agg implements IteratorAggregate {
    public $pub = 'p';
    private $priv = 'q';
    function getIterator(): Iterator { return new ArrayIterator([9]); }
}
$o = new P; $n = 0;
foreach ($o as $k => &$v) { echo "to aggregate: $k\n"; if (++$n == 1) $o = new Agg; if ($n > 8) { echo "LOOP\n"; break; } }

// A Traversable subject is walked through its iterator, which does not
// re-read the variable.
$o = new ArrayIterator([1, 2, 3]); $n = 0;
foreach ($o as $k => &$v) { echo "iterator: $k\n"; if (++$n == 1) $o = new Q; if ($n > 8) { echo "LOOP\n"; break; } }

// By value, nothing is re-read.
$o = new P; $n = 0;
foreach ($o as $k => $v) { echo "by value: $k\n"; if (++$n == 1) $o = new Q; if ($n > 8) { echo "LOOP\n"; break; } }

// Inside a function the local is held as a box as well: the assignment in
// the body is what the next fetch reads.
function inFunction() {
    $o = new P; $n = 0;
    foreach ($o as $k => &$v) { echo "function: $k\n"; if (++$n == 1) $o = new Q; if ($n > 8) { echo "LOOP\n"; break; } }
}
inFunction();

// Private properties show to a walk in the class's scope, re-read included.
class S {
    private $p1 = 'a';
    public $p2 = 'b';
    static function run() {
        $o = new S; $n = 0;
        foreach ($o as $k => &$v) { echo "scope: $k\n"; if (++$n == 1) $o = new S; if ($n > 8) { echo "LOOP\n"; break; } }
    }
}
S::run();
$o = new S; $n = 0;
foreach ($o as $k => &$v) { echo "outside: $k\n"; if (++$n == 1) $o = new S; if ($n > 8) { echo "LOOP\n"; break; } }

// A lazy ghost handed to the loop is initialised by the next fetch.
class C {
    public int $a = 0;
    public $b = 'x';
    public function __construct() { echo "ctor\n"; $this->a = 1; }
}
$r = new ReflectionClass(C::class);
$o = new P; $n = 0;
foreach ($o as $k => &$v) {
    echo "ghost: $k\n";
    if (++$n == 1) $o = $r->newLazyGhost(function ($g) { echo "init\n"; $g->__construct(); });
    if ($n > 8) { echo "LOOP\n"; break; }
}
// ... and one whose initializer throws, throws at the foreach.
$o = new P;
try {
    foreach ($o as $k => &$v) {
        echo "throwing: $k\n";
        $o = $r->newLazyGhost(function ($g) { throw new Exception("from initializer"); });
    }
} catch (Exception $e) {
    echo get_class($e), ": ", $e->getMessage(), " at line ", $e->getLine(),
        ", called from line ", $e->getTrace()[0]['line'], "\n";
}
// A lazy proxy is walked through its instance, and handing back the proxy
// goes on.
$p = $r->newLazyProxy(function ($g) { echo "proxy init\n"; return new C(); });
$o = new P; $n = 0;
foreach ($o as $k => &$v) {
    echo "proxy: $k\n";
    $v = is_int($v) ? $v + 100 : "w$k";
    ++$n;
    if ($n == 1) $o = $p;
    if ($n == 3) $o = $p;
    if ($n > 8) { echo "LOOP\n"; break; }
}
unset($v);
var_dump(get_object_vars($p));

// **A fetch for writing forwarded by an initialized proxy to an instance
// whose __get is running for that name reads instead** (gh21478): the
// "Undefined property" warning, a temporary NULL, nothing created.
class Foo {
    public $_;
    public function &__get($name) {
        global $target;
        echo "get($name) on ", $this::class, "\n";
        return $target->{$name};
    }
}
class Bar extends Foo {}
$rc = new ReflectionClass(Bar::class);
$target = $rc->newLazyProxy(function () { return new Foo(); });
$real = $rc->initializeLazyObject($target);
$y = &$real->y;
var_dump($y, property_exists($real, 'y'));

// When the `__get` running is the proxy's own, the forwarded fetch creates
// the property on the instance (gh20854).
class Own {
    public $prop;
    function &__get($name) {
        echo "own($name)\n";
        return $this->x;
    }
}
$rc = new ReflectionClass(Own::class);
$own = $rc->newLazyProxy(function () { echo "own init\n"; return new Own; });
$own->x;
$own2 = $rc->newLazyProxy(function () { echo "own init 2\n"; return new Own; });
$ownInst = $rc->initializeLazyObject($own2);
$own2->y;
var_dump(property_exists($ownInst, 'x'), property_exists($ownInst, 'y'));

// Without the proxy the same fetch creates the property.
$direct = new Foo; $target = $direct;
$x = &$direct->x;
var_dump($x, property_exists($direct, 'x'));

// As an array container too.
class U {
    public $_;
    public function &__get($n) {
        global $up;
        echo "u($n)\n";
        $up->{$n}[] = 1;
        $x = 0;
        return $x;
    }
}
class U2 extends U {}
$rc = new ReflectionClass(U2::class);
$up = $rc->newLazyProxy(function () { return new U(); });
$ur = $rc->initializeLazyObject($up);
$c = &$ur->k;
var_dump(get_object_vars($ur));

// An initialized proxy with no __get running creates, and one whose
// instance has an idle __get asks it.
class N { public $_; }
class N2 extends N {}
$rc = new ReflectionClass(N2::class);
$np = $rc->newLazyProxy(function () { return new N(); });
$ni = $rc->initializeLazyObject($np);
$z = &$np->nope;
var_dump(get_object_vars($ni));
class G { public $_; public function &__get($n) { echo "g($n)\n"; $x = 42; return $x; } }
class G2 extends G {}
$rc = new ReflectionClass(G2::class);
$gp = $rc->newLazyProxy(function () { return new G(); });
$rc->initializeLazyObject($gp);
$w = &$gp->nope;
var_dump($w);
