<?php
// The Serializable format of SplDoublyLinkedList, SplObjectStorage and
// ArrayObject: one back-reference table per call, joined to a running
// unserialize() for a C: payload, fresh inside a hook; the error offsets;
// ArrayObject's flags, storage replacement and __serialize pair.

function attempt(callable $f) {
    try {
        $f();
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

$o = new stdClass;
$o->v = 1;
$l = new SplQueue;
$l->push($o);
$l->push([$o, 1]);
$l->push($o);
echo $s = $l->serialize(), "\n";
$m = new SplQueue;
$m->unserialize($s);
var_dump($m[0] === $m[2], $m[1][0] === $m[0]);

// An element whose serialization takes the next one out of the list.
class Popper {
    function __serialize(): array {
        global $list;
        $list->pop();
        return [];
    }
}
$list = new SplDoublyLinkedList;
$list->push(new Popper);
$list->push(1);
var_dump($list->serialize());

// A C: payload read inside unserialize() numbers its values in the outer
// table; a hook's own session starts afresh.
$p = 'i:4;:r:2;:N;:O:8:"stdClass":1:{s:1:"a";r:2;}';
$v = unserialize('a:3:{i:0;O:8:"stdClass":0:{}i:1;C:19:"SplDoublyLinkedList":' . strlen($p) . ':{' . $p . '}i:2;r:2;}');
var_dump($v[1][0] === $v[0], $v[1][2]->a === $v[0], $v[2] === $v[0]);
class Holder {
    public $l;
    function __serialize(): array { return [$this->l->serialize()]; }
    function __unserialize(array $d): void { $this->l = new SplStack; $this->l->unserialize($d[0]); }
}
$h = new Holder;
$h->l = $l;
echo $t = serialize([$o, $h]), "\n";
$back = unserialize($t);
var_dump($back[1]->l[0] === $back[1]->l[2]);

foreach (['i:0;:i:1;:x', 'i:0;:a:2:{i:0;i:1;}', 'i:0;:s:5:"ab";', 'x', 'i:0', 'b:1', 'b:1;:i:1;', 'i:0;:i:1;q', 'i:0;:'] as $bad) {
    $d = new SplDoublyLinkedList;
    $d->push(9);
    attempt(fn() => $d->unserialize($bad));
    echo count($d), "\n";
}

$st = new SplObjectStorage;
$st[$o] = [1];
$st[new stdClass] = null;
echo $ss = $st->serialize(), "\n";
$st2 = new SplObjectStorage;
$st2->unserialize($ss);
var_dump(count($st2), $st2->serialize() === $ss);
foreach (['x:i:1;O:8:"stdClass":0:{},i:5;;m:a:0:{}', 'x:i:2;O:8:"stdClass":0:{},N;;r:2;,N;;m:a:0:{}', 'x:i:1;i:5,N;;m:a:0:{}', 'zz', 'x:i:0;m:a:0:{}', 'x:i:-1;m:a:0:{}', 'x:i:0;q', 'x:i:0;;m:a:0:{}', 'x:i:2;O:8:"stdClass":0:{},N;O:8:"stdClass":0:{},N;;m:a:0:{}'] as $bad) {
    $s3 = new SplObjectStorage;
    attempt(fn() => $s3->unserialize($bad));
    echo count($s3), "\n";
}

class Props extends SplObjectStorage {
    public $pub = 1;
    private $priv = 2;
}
$pr = new Props;
echo str_replace("\0", "~", serialize($pr)), "\n";
echo str_replace("\0", "~", $pr->serialize()), "\n";
var_dump(unserialize(serialize($pr)) == $pr);

// ArrayObject and ArrayIterator.
$ao = new ArrayObject([1, 2], ArrayObject::STD_PROP_LIST);
var_dump($ao->getFlags());
$ao->setFlags(0);
echo serialize($ao), "\n", serialize(new ArrayIterator(['k' => 'v'], 1)), "\n";
echo $as = $ao->serialize(), "\n";
$ao2 = new ArrayObject;
$ao2->unserialize($as);
var_dump($ao2->getArrayCopy(), unserialize(serialize($ao))->getArrayCopy());
var_dump($ao->exchangeArray(['x' => 1]), count($ao));
class U {
    public $a = 1;
}
$u = new U;
$old = $ao->exchangeArray($u);
var_dump($old, count($ao));
echo serialize($ao), "\n";
attempt(fn() => $ao->exchangeArray(5));
attempt(fn() => $ao->exchangeArray(new SplFixedArray(1)));
foreach (['x:i:0;a:1:{i:0;i:1;};m:a:0:{}', 'x:i:0;a:1:{i:0;i:1;};m:a:1:{s:1:"p";i:5;}', 'y', 'x:i:0;N;', 'x:i:0;a:0:{}', 'x:i:0;r:1;;m:a:0:{}'] as $bad) {
    $a = new ArrayObject([7]);
    attempt(fn() => $a->unserialize($bad));
    echo json_encode($a->getArrayCopy()), json_encode(get_object_vars($a)), "\n";
}
class MyAO extends ArrayObject {
    private $hidden = 3;
    public $shown = 4;
}
$my = new MyAO(['q']);
var_dump($my->__serialize());
$my2 = unserialize(serialize($my));
var_dump($my2 == $my, $my2->getArrayCopy());
attempt(fn() => unserialize('O:11:"ArrayObject":0:{}'));
attempt(fn() => unserialize('O:11:"ArrayObject":3:{i:0;i:0;i:1;i:0;i:2;a:0:{}}'));
attempt(fn() => unserialize('O:11:"ArrayObject":4:{i:0;i:0;i:1;a:0:{}i:2;a:0:{}i:3;s:5:"Nopes";}'));
attempt(fn() => unserialize('O:11:"ArrayObject":4:{i:0;i:0;i:1;a:0:{}i:2;a:0:{}i:3;s:1:"U";}'));
$f = new ArrayObject([1], ArrayObject::STD_PROP_LIST);
var_dump(unserialize(serialize($f))->getFlags());
$g = new ArrayObject;
$g->unserialize($f->serialize());
var_dump($g->getFlags());
