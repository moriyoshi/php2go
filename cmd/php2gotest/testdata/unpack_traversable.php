<?php
// A Traversable unpacks by being **drained**: one left-to-right walk of the
// iterator protocol.
function take($a, $b, $c = "dflt") { var_dump($a, $b, $c); }
function gInt() { yield 1; yield 2; }
take(...gInt(), ...[3]);

// **An integer key is not a position.** A generator yielding `5 => "x"` then
// `9 => "y"` sends them as the next two arguments, not the sixth and tenth.
function gSkip() { yield 5 => "x"; yield 9 => "y"; }
take(...gSkip());

// Being Traversable is what matters, not being a Generator: a user Iterator
// drains the same way. (ArrayIterator would do too, but the compiled runtime
// does not model it yet.)
class Pair implements Iterator {
    private $at = 0;
    private $items = ["p", "q"];
    public function current(): mixed { return $this->items[$this->at]; }
    public function key(): mixed { return $this->at; }
    public function next(): void { $this->at++; }
    public function rewind(): void { $this->at = 0; }
    public function valid(): bool { return $this->at < count($this->items); }
}
take(...new Pair);

// A key that is neither int nor string stops the unpack.
function gFloat() { yield 1.5 => "x"; }
try { take(...gFloat()); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A by-reference parameter gets a **warning**, not an error, and the call still
// happens -- a Traversable hands out values, not boxes.
function byref($x, &$r) { $r = "changed"; }
$holder = "before";
byref(...gInt());
var_dump($holder);

// Unpacking something that is neither an array nor a Traversable.
try { take(...new stdClass); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
