<?php
// Only an Iterator's rewind() suspends, and nothing else in the program does:
// no generator body suspends a fiber and no method named like the driver call
// does. The fiber's `$gen->current()` still has to be split, because the
// generator it drives `yield from`s an object and the object's rewind() may
// suspend (gh9916-007/008's shape). Unsplit, the suspension crossed the
// generator driver and was refused.
$it = new class implements Iterator {
    private $i = 0;
    public function current(): mixed { return "v{$this->i}"; }
    public function key(): mixed { return $this->i; }
    public function next(): void { $this->i++; }
    public function rewind(): void {
        try {
            echo "rewind\n";
            $got = Fiber::suspend('from rewind');
            echo "rewind got $got\n";
        } finally {
            echo "rewind finally\n";
        }
    }
    public function valid(): bool { return $this->i < 2; }
};

$gen = (function () use ($it) {
    try {
        $r = yield from $it;
        var_dump($r);
    } finally {
        echo "gen finally\n";
    }
})();
$fiber = new Fiber(function () use ($gen) {
    var_dump($gen->current());
    $gen->next();
    var_dump($gen->key(), $gen->current());
    $gen->next();
    var_dump($gen->valid());
});
var_dump($fiber->start());
var_dump($fiber->resume('R'));
var_dump($fiber->isTerminated());
