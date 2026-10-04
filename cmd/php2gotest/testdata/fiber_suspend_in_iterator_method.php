<?php
// A `Fiber::suspend()` inside an Iterator's own method, reached through a
// generator's `yield from` (gh9916-007's shape). The methods were ordinary
// calls from the delegation, so the suspension had nowhere to park and was
// refused as "call to rewind, which suspends into its caller". A hidden
// generator now drives the Iterator: a method whose body may suspend is
// entered as a frame, the hidden generator parks, and the delegating one
// parks behind it -- the same chain a generator source makes.
//
// Covered: the protocol order (rewind, valid, current, key per element, next
// and valid per advance) around a suspension in each method, an
// IteratorAggregate handing back such an Iterator, keys and send, a throw into
// the fiber unwinding the method first, a trace after the resume, and a fiber
// destroyed at shutdown while suspended in a method: its finally blocks run,
// the method's before the generator's.
class It implements Iterator {
    private $i = 0;
    public function rewind(): void {
        echo "rewind\n";
        $got = Fiber::suspend('in rewind');
        echo "rewind resumed with $got\n";
        $this->i = 0;
    }
    public function valid(): bool { echo "valid {$this->i}\n"; return $this->i < 3; }
    public function current(): mixed { echo "current\n"; return "v{$this->i}"; }
    public function key(): mixed { echo "key\n"; return "k{$this->i}"; }
    public function next(): void {
        echo "next\n";
        if ($this->i == 1) {
            $got = Fiber::suspend('in next');
            echo "next resumed with $got\n";
        }
        $this->i++;
    }
}
function gen() {
    $r = yield from new It();
    var_dump($r);
    yield 'tail' => 'end';
    return 'done';
}
$g = gen();
$f = new Fiber(function () use ($g) {
    var_dump($g->current());
    var_dump($g->key());
    $g->next();
    var_dump($g->current());
    $g->next();
    var_dump($g->key(), $g->current());
    var_dump($g->send('ignored'));
    $g->next();
    var_dump($g->key(), $g->current());
    $g->next();
    var_dump($g->valid(), $g->getReturn());
    return 'fiber done';
});
var_dump($f->start());
var_dump($f->resume('A'));
var_dump($f->resume('B'));
var_dump($f->getReturn());

// A throw into the fiber while a method is suspended unwinds the method first.
class Thrower implements Iterator {
    public function rewind(): void {
        try {
            Fiber::suspend('parked');
        } finally {
            echo "rewind finally\n";
        }
    }
    public function valid(): bool { return true; }
    public function current(): mixed { return 1; }
    public function key(): mixed { return 0; }
    public function next(): void {}
}
function outer() {
    try {
        yield from new Thrower();
    } catch (Exception $e) {
        echo "outer caught ", $e->getMessage(), "\n";
        yield 'recovered';
    }
}
$g2 = outer();
$f2 = new Fiber(function () use ($g2) {
    var_dump($g2->current());
});
var_dump($f2->start());
$f2->throw(new Exception('boom'));
var_dump($f2->isTerminated());

// An exception from a suspended method after resuming, traced.
class Bad implements Iterator {
    public function rewind(): void {
        Fiber::suspend();
        throw new LogicException('bad rewind');
    }
    public function valid(): bool { return true; }
    public function current(): mixed { return 1; }
    public function key(): mixed { return 0; }
    public function next(): void {}
}
function third() {
    yield from new Bad();
}
$f3 = new Fiber(function () {
    $g3 = third();
    try {
        $g3->current();
    } catch (LogicException $e) {
        echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
    }
});
$f3->start();
$f3->resume();
class Loud implements Iterator {
    private $i = 0;
    public function __construct(private string $where) {}
    private function maybe($m) {
        if ($m === $this->where) {
            echo "suspend in $m\n";
            $v = Fiber::suspend($m);
            echo "$m got $v\n";
        }
    }
    public function rewind(): void { $this->maybe('rewind'); $this->i = 0; }
    public function valid(): bool { if ($this->i == 1) $this->maybe('valid'); return $this->i < 2; }
    public function current(): mixed { $this->maybe('current'); return $this->i * 10; }
    public function key(): mixed { $this->maybe('key'); return $this->i; }
    public function next(): void {
        try { $this->maybe('next'); } finally { echo "next finally\n"; }
        $this->i++;
    }
}
class Agg implements IteratorAggregate {
    public function getIterator(): Iterator { echo "getIterator\n"; return new Loud('rewind'); }
}
function g($src) {
    try {
        yield 'before' => 1;
        $r = yield from $src;
        yield 'after' => 2;
    } finally {
        echo "g finally\n";
    }
}
foreach (['rewind', 'valid', 'current', 'key', 'next', 'agg'] as $where) {
    echo "== $where\n";
    $gen = g($where === 'agg' ? new Agg : new Loud($where));
    $f = new Fiber(function () use ($gen) {
        foreach ([1, 2, 3, 4, 5] as $_) {
            if (!$gen->valid()) break;
            echo $gen->key(), " => ", $gen->current(), "\n";
            $gen->next();
        }
        echo "done\n";
    });
    $v = $f->start();
    while (!$f->isTerminated()) {
        echo "fiber gave $v\n";
        $v = $f->resume("R");
    }
}

// Destroyed at shutdown while suspended in next(): next's finally, then g's.
$gen = g(new Loud('next'));
$held = new Fiber(function () use ($gen) {
    $gen->current();
    $gen->next();
    $gen->next();
    echo "not reached\n";
});
var_dump($held->start());
echo "end\n";
