<?php
// A list() that overwrites a variable releases the object it held, when every
// value the variable ever holds is either a fresh object or provably holds no
// object at any depth (temporary_cleaning_011).
class A {
    function __construct(public $n) {}
    function __destruct() { echo "dA{$this->n}\n"; }
}
class T {
    function __destruct() { throw new Exception("from dtor"); }
}

// From a literal array.
$c = new A('c');
[$x, $c] = [1, 2];
echo "after literal list\n";

// From a nested literal.
$d = new A('d');
list($y, $d) = [[5, 6]][0];
echo "after nested list\n";

// From a local built only from scalars.
$q = 0;
$src = [[$q, $q]];
$e = new A('e');
list($z, $e) = $src[0];
echo "after local list\n";

// A destructor that throws at the list assignment is caught there.
$t = new T;
$zero = 0;
$pairs = [[$zero, $zero]];
try {
    list($u, $t) = $pairs[0];
} catch (Exception $ex) {
    echo "caught: ", $ex->getMessage(), "\n";
}

// Negative: an element read from an array that holds an object gives that
// object a second owner, so overwriting the variable afterwards frees nothing.
$keep = [new A('k')];
$f = $keep[0];
[$f] = [1];
echo "k still held\n";

// Negative: a function rebinds the source array as a global and puts an
// object in it; the element read shares it.
$shared = [1];
function fill() { global $shared; $shared = [new A('in')]; }
fill();
$g = new stdClass;
[$g] = $shared;
$g = 0;
echo "in still held\n";

// Negative: a reference into the source array.
$src2 = [1];
$r = &$src2;
$r[0] = new A('ref');
$h = new stdClass;
[$h] = $src2;
$h = 0;
echo "ref still held\n";

// Negative: a dimension write into the source array.
$src3 = [1];
$src3[0] = new A('dw');
$w = new stdClass;
[$w] = $src3;
$w = 0;
echo "dw still held\n";

// No global holds an object directly -- only arrays do -- so what is left is
// destroyed by the objects-store walk, in handle order.
echo "end\n";
