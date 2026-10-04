<?php
// Two arrays are ordered element by element, as zend_hash_compare does: the
// smaller count first, then each element of the left in its order against
// the right's under the same key, a missing key making the pair
// uncomparable. A nested object beside a string or a number converts as it
// would at the top level (gh19305).

class S { public function __toString() { echo "ts\n"; return 'x'; } }
class T { public $p; function __construct($p) { $this->p = $p; } }
class R {
    public function __toString() {
        global $ra, $rb;
        echo "rel\n";
        $ra = $rb = null;
        return '';
    }
}

$a = [1, 2]; $b = [1, 3];
var_dump($a < $b, $b <=> $a, $a == $b, $a >= $b);
$a = [1, [2, 3]]; $b = [1, [2, 4]];
var_dump($a < $b, $a <=> $b, $b > $a);
$a = ['a' => 1, 'b' => 2]; $b = ['b' => 2, 'a' => 1]; $c = ['a' => 1, 'c' => 2];
var_dump($a <=> $b, $a <=> $c, $c <=> $a, $a < $c, $c < $a, $a == $b);
$x = [new T(1)]; $y = [new T(2)];
var_dump($x < $y, $x <=> $y);
$x = [5]; $y = [new T(1)];
var_dump($x < $y, $x <=> $y);
var_dump([null] <=> [0], [false] <=> [[]], ['abc'] <=> [0], [NAN] <=> [1]);

$a = ['x', 2]; $b = [new S, 2];
var_dump($a == $b, $a <=> $b, $a != $b);
$a = (object)['foo' => 'test', 'bar' => 2];
$b = (object)['foo' => new S, 'bar' => 2];
var_dump($a > $b, $a < $b, $a == $b, $a <=> $b);

$ra = (object)['foo' => 'test', 'bar' => 2];
$rb = (object)['foo' => new R, 'bar' => 2];
var_dump($ra > $rb);
var_dump($ra, $rb);

$m = [[3, 1], [1, 2], [1, 1], [2], [1, 1, 0]];
sort($m);
echo json_encode($m), "\n";
rsort($m);
echo json_encode($m), "\n";
