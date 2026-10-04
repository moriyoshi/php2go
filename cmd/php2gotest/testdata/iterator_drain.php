<?php
// iterator_to_array and iterator_count over arrays, generators and user
// Iterators: keys converted as array keys, and counting asks neither
// current() nor key().
function g() {
    yield 1 => "a";
    yield 1 => "b";
    yield 2.5 => "c";
}
var_dump(iterator_to_array(g()), iterator_to_array(g(), false), iterator_count(g()));
var_dump(iterator_count([1, 2, 3]), iterator_to_array(["a" => 1, 5 => 2], false));

class Loud implements Iterator {
    private $i = 0;
    function current(): mixed { echo "current\n"; return $this->i * 10; }
    function key(): mixed { echo "key\n"; return "k" . $this->i; }
    function next(): void { echo "next\n"; $this->i++; }
    function rewind(): void { echo "rewind\n"; $this->i = 0; }
    function valid(): bool { echo "valid\n"; return $this->i < 2; }
}
var_dump(iterator_count(new Loud));
var_dump(iterator_to_array(new Loud));
var_dump(iterator_to_array(new Loud, false));
class Agg implements IteratorAggregate {
    function getIterator(): Iterator { return new ArrayIterator(["x" => 1]); }
}
var_dump(iterator_to_array(new Agg));
foreach ([5, new stdClass, "s"] as $bad) {
    foreach (["iterator_to_array", "iterator_count"] as $fn) {
        try {
            $fn($bad);
        } catch (TypeError $e) {
            echo $e->getMessage(), "\n";
        }
    }
}
