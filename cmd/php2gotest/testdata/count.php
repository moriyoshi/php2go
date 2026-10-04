<?php
// count() is an intrinsic: arrays answer directly, a Countable re-enters its
// own count(), and PHP 8 takes nothing else — the TypeError names the CLASS for
// an object rather than "object".

var_dump(count([]), count([1, 2, 3]), count([[1, 2], [3]]));
var_dump(count(["a" => 1, "b" => 2]));

class Counted implements Countable {
    public function __construct(private int $n) {}
    public function count(): int {
        echo "[counting]";
        return $this->n;
    }
}
var_dump(count(new Counted(4)));
var_dump(count(new Counted(0)));

foreach ([null, "ab", 1, 1.5, true, new stdClass()] as $bad) {
    try {
        var_dump(count($bad));
    } catch (TypeError $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

// A count() that throws propagates.
class BadCount implements Countable {
    public function count(): int { throw new LogicException("nope"); }
}
try { var_dump(count(new BadCount())); } catch (LogicException $e) { echo "caught: ", $e->getMessage(), "\n"; }
