<?php
// `foreach` over an array and over a Traversable are the same PHP type -- an
// object holding a cursor -- with completely different effect contracts, and
// the compiler narrows only the array case.
//
// Every method here announces itself, so the call **sequence** is graded, not
// just the values: the engine runs `rewind, valid, current, key, [body], next,
// valid, ...` and a step in the wrong place shows up as a reordered line.
//
// What this file covers is the narrowed path being *correct*: keys that are not
// sequential ints, a subject that may be null and so must not narrow, and the
// key/value binding.
function walkArray(array $a) {
    foreach ($a as $k => $v) {
        echo "  body $k=$v\n";
    }
}

// The subject may be null, so this takes the general path.
function walkMaybe(?array $a) {
    foreach ($a as $k => $v) {
        echo "  maybe $k=$v\n";
    }
}

// Value-only binding: the engine never calls `key` when the loop binds no key,
// so the narrowed path must not either.
function valuesOnly(array $a) {
    foreach ($a as $v) {
        echo "  v=$v\n";
    }
}

walkArray(["a" => 1, "b" => 2]);
walkMaybe(["c" => 3]);
walkArray([5 => "five", "s" => "str", 6 => "six"]);
walkArray([]);
valuesOnly([10, 20]);
// Nested, so the inner loop's cursor is a second array-backed one.
function nested(array $rows) {
    foreach ($rows as $row) {
        foreach ($row as $cell) {
            echo "  cell=$cell\n";
        }
    }
}
nested([[1, 2], [3]]);

class Loud implements Iterator {
    private $i = 0;
    private $a = ["x", "y", "z"];
    public function rewind(): void { echo "rewind\n"; $this->i = 0; }
    public function valid(): bool { echo "valid({$this->i})\n"; return $this->i < 3; }
    public function current(): mixed { echo "current({$this->i})\n"; return $this->a[$this->i]; }
    public function key(): mixed { echo "key({$this->i})\n"; return $this->i; }
    public function next(): void { echo "next({$this->i})\n"; $this->i = $this->i + 1; }
}

class Agg implements IteratorAggregate {
    public function getIterator(): Iterator { echo "getIterator\n"; return new Loud(); }
}

echo "--- iterator, keyed:\n";
foreach (new Loud() as $k => $v) { echo "  body $k=$v\n"; }
// `key()` is not called at all when the loop binds no key.
echo "--- iterator, unkeyed:\n";
foreach (new Loud() as $v) { echo "  body $v\n"; }
echo "--- aggregate:\n";
foreach (new Agg() as $k => $v) { echo "  agg $k=$v\n"; }
// `break` leaves the iterator where it is: the engine does not step on the way
// out, which a step placed before the body would have done.
echo "--- break:\n";
foreach (new Loud() as $v) { echo "  body $v\n"; break; }
// `continue` is a back edge and *does* step.
echo "--- continue:\n";
foreach (new Loud() as $v) { if ($v == "y") { continue; } echo "  body $v\n"; }

// A plain object iterates its **visible** properties, and which those are
// depends on the calling scope: from outside the class only the public ones.
// runtime/php reported this as a gap because it has no scope to consult, so the
// answer is built in the generated unit, which has one.
class Props {
    public $pub = 1;
    protected $prot = 2;
    private $priv = 3;
    public function inside() {
        echo "-- inside:\n";
        foreach ($this as $k => $v) { echo "  $k=$v\n"; }
    }
}

echo "-- outside:\n";
foreach (new Props() as $k => $v) { echo "  $k=$v\n"; }
(new Props())->inside();

$o = new stdClass();
$o->a = 1;
$o->b = "two";
echo "-- stdClass:\n";
foreach ($o as $k => $v) { echo "  $k=$v\n"; }
