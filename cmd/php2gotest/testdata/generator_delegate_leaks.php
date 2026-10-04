<?php
// An Iterator handed to `yield from` whose methods store `$this` is not the
// delegation's alone: it outlives the generator's body. (Its own file: the
// store is summarised per method name, so it would refuse every other
// Iterator in the same program too.)

class Leaky implements Iterator {
    public static $seen = [];
    public static bool $loud = true;
    private int $i = 0;
    public function current(): mixed {
        self::$seen[] = $this;
        return $this->i;
    }
    public function key(): mixed { return $this->i; }
    public function next(): void { $this->i++; }
    public function rewind(): void { $this->i = 0; }
    public function valid(): bool { return $this->i < 2; }
    public function __destruct() {
        if (self::$loud) {
            echo "~Leaky\n";
        }
    }
}

function leaks() {
    $it = new Leaky();
    try {
        yield from $it;
    } finally {
        echo "leaks: finally\n";
    }
}
foreach (leaks() as $k => $v) {
    echo "$k => $v\n";
}
echo "after leaks: ", count(Leaky::$seen), "\n";

Leaky::$loud = false;
echo "end\n";
