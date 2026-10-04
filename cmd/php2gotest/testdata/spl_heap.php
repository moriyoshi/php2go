<?php
// SplHeap, SplMinHeap, SplMaxHeap and SplPriorityQueue: the storage order a
// dump shows, the comparisons a user compare() sees, corruption, the write
// lock and the extraction flags.

function attempt(callable $f) {
    try {
        $f();
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

$max = new SplMaxHeap;
foreach ([5, 1, 8, 3, 9, 2, 8] as $v) {
    $max->insert($v);
}
print_r($max);
var_dump($max->top(), $max->extract(), count($max), $max->key(), $max->current());
print_r($max);
foreach ($max as $k => $v) {
    echo "$k:$v ";
}
echo count($max), "\n";
var_dump($max->valid(), $max->current(), $max->isEmpty());
attempt(fn() => $max->top());
attempt(fn() => $max->extract());

$min = new SplMinHeap;
foreach (["b", "a", "c", 10, 9.5] as $v) {
    $min->insert($v);
}
$copy = clone $min;
while ($min->valid()) {
    var_dump($min->current());
    $min->next();
}
var_dump(count($copy), $copy->top());
attempt(fn() => $copy->compare(1, 2));

// A user compare() sees every comparison, in the engine's order.
class Loud extends SplHeap {
    protected function compare($a, $b): int {
        echo "cmp($a,$b) ";
        return $a <=> $b;
    }
}
$loud = new Loud;
foreach ([3, 1, 4, 1, 5] as $v) {
    $loud->insert($v);
    echo "\n";
}
echo $loud->extract(), "\n";
print_r($loud);

// A compare() that throws corrupts the heap.
class Fragile extends SplMinHeap {
    public $fail = false;
    protected function compare($a, $b): int {
        if ($this->fail) {
            throw new Exception("cmp failed");
        }
        return parent::compare($a, $b);
    }
}
$f = new Fragile;
$f->insert(2);
$f->insert(1);
$f->fail = true;
attempt(fn() => $f->insert(0));
var_dump($f->isCorrupted(), count($f));
attempt(fn() => $f->top());
attempt(fn() => $f->insert(5));
attempt(function () use ($f) {
    foreach ($f as $v) {
    }
});
$f->recoverFromCorruption();
$f->fail = false;
var_dump($f->isCorrupted(), $f->extract());
print_r($f);

// A compare() that modifies the heap it compares for.
class Meddling extends SplMaxHeap {
    protected function compare($a, $b): int {
        try {
            $this->insert(0);
        } catch (RuntimeException $e) {
            echo $e->getMessage(), "\n";
        }
        return parent::compare($a, $b);
    }
}
$m = new Meddling;
$m->insert(1);
$m->insert(2);
var_dump(count($m));

attempt(fn() => new SplHeap);
attempt(fn() => (new SplMinHeap)->insert());

$pq = new SplPriorityQueue;
$pq->insert("lo", 1);
$pq->insert("hi", 10);
$pq->insert("mid", 5);
$pq->insert("mid2", 5);
$pq->insert("arr", [1]);
print_r($pq);
var_dump($pq->top(), $pq->getExtractFlags());
$pq->setExtractFlags(SplPriorityQueue::EXTR_BOTH);
var_dump($pq->extract());
$pq->setExtractFlags(SplPriorityQueue::EXTR_PRIORITY);
var_dump($pq->current(), $pq->compare(1, 2));
attempt(fn() => $pq->setExtractFlags(4));
$pq->setExtractFlags(SplPriorityQueue::EXTR_DATA);
foreach ($pq as $k => $v) {
    echo "$k:$v ";
}
echo count($pq), "\n";
attempt(fn() => $pq->insert(1));
echo serialize(new SplMinHeap), serialize($pq), "\n";

class Counted extends SplMaxHeap {
    function count(): int { return 42; }
}
$c = new Counted;
var_dump(count($c), $c->isEmpty());
