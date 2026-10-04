<?php
// SplDoublyLinkedList, SplQueue and SplStack: storage, the traverse pointer,
// the iteration modes, the exceptions and the dumps.

function attempt(callable $f) {
    try {
        $f();
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

// A temporary container is freed after the call.
(new SplStack)->push(1);
(new SplMaxHeap)->insert(1);
var_dump(spl_object_id(new stdClass));

$l = new SplDoublyLinkedList;
$l->push(1);
$l->push(2);
$l->unshift(0);
$l[] = 3;
$l->add(2, "x");
$l->add(5, "end");
var_dump(count($l), $l->top(), $l->bottom(), $l[2], isset($l[5]), isset($l[6]), $l->isEmpty());
$l[2] = "y";
unset($l[0]);
print_r($l);
attempt(fn() => $l[9]);
attempt(fn() => $l["a"]);
attempt(fn() => $l->offsetSet(-1, 1));
attempt(fn() => $l->add(9, 1));
attempt(fn() => $l->push());
attempt(fn() => (new SplDoublyLinkedList)->pop());
attempt(fn() => (new SplDoublyLinkedList)->shift());
attempt(fn() => (new SplDoublyLinkedList)->top());
attempt(fn() => (new SplQueue)->dequeue());

// The pointer moved by hand, in both directions.
for ($l->rewind(); $l->valid(); $l->next()) {
    echo $l->key(), "=>", $l->current(), " ";
}
echo "\n";
$l->setIteratorMode(SplDoublyLinkedList::IT_MODE_LIFO);
for ($l->rewind(); $l->valid(); $l->next()) {
    echo $l->key(), "=>", $l->current(), " ";
}
echo "\n";
$l->rewind();
$l->prev();
var_dump($l->key(), $l->current(), $l[0]);

// The element the pointer stands on is popped: the pointer stays on it.
$p = new SplDoublyLinkedList;
foreach ([1, 2, 3] as $v) {
    $p->push($v);
}
$p->rewind();
$p->next();
$p->next();
var_dump($p->pop(), $p->valid(), $p->current(), $p->key());
$p->next();
var_dump($p->valid());
$p->rewind();
unset($p[0]);
var_dump($p->valid(), $p->current());

// foreach takes its own copy of the pointer.
$q = new SplQueue;
$q->enqueue("a");
$q->enqueue("b");
$q->enqueue("c");
$q->rewind();
$q->next();
foreach ($q as $k => $v) {
    echo "$k:$v ";
}
echo $q->current(), "\n";
attempt(fn() => $q->setIteratorMode(SplDoublyLinkedList::IT_MODE_LIFO));
var_dump($q->setIteratorMode(SplDoublyLinkedList::IT_MODE_DELETE), $q->getIteratorMode());
foreach ($q as $k => $v) {
    echo "$k:$v ";
}
echo count($q), "\n";

$s = new SplStack;
foreach ([1, 2, 3, 4] as $v) {
    $s->push($v);
}
foreach ($s as $k => $v) {
    echo "$k:$v ";
}
echo "\n";
$s->setIteratorMode(SplDoublyLinkedList::IT_MODE_LIFO | SplDoublyLinkedList::IT_MODE_DELETE);
foreach ($s as $k => $v) {
    echo "$k:$v ";
    if ($v == 3) {
        break;
    }
}
echo count($s), " ", $s->top(), "\n";
print_r($s);

// A clone has its own list and its pointer back at the head.
$c = clone $s;
$c->push(9);
var_dump(count($s), count($c), $c->current());

// Serialization, both formats.
class Named extends SplStack {
    public $pub = 1;
    protected $prot = 2;
    private $priv = 3;
}
$n = new Named;
$n->push("a");
$n->push([1, 2]);
echo str_replace("\0", "~", serialize($n)), "\n";
var_dump($n->__serialize());
$plain = new SplQueue;
$plain->push(1.5);
$plain->push(["k" => true, null]);
echo $str = $plain->serialize(), "\n";
$back = new SplQueue;
$back->unserialize($str);
print_r($back);
var_dump(unserialize(serialize($plain)) == $plain);
attempt(fn() => (new SplStack)->__unserialize([]));
attempt(fn() => (new SplStack)->unserialize("i:0;:i:1;x"));

// A subclass's count() answers isEmpty() and count(); one overriding an
// Iterator method makes foreach call the methods.
class Counted extends SplDoublyLinkedList {
    function count(): int { echo "count() "; return 0; }
}
$cnt = new Counted;
$cnt->push(1);
var_dump($cnt->isEmpty(), count($cnt));
class Loud extends SplDoublyLinkedList {
    function current(): mixed { echo "current() "; return parent::current(); }
}
$loud = new Loud;
$loud->push("q");
foreach ($loud as $v) {
    echo $v, "\n";
}
attempt(function () use ($l) {
    foreach ($l as &$v) {
    }
});
