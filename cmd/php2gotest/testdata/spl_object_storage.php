<?php
// SplObjectStorage, natively: identity keys, the internal iterator, infos,
// refusals, dump shape and __serialize/__unserialize.
function t($f) {
    try {
        $r = $f();
        var_dump($r);
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
class H extends SplObjectStorage { public $z = 3; }
$h = new H;
$h->attach(new stdClass);
var_dump($h, serialize($h));

$s = new SplObjectStorage;
$a = new stdClass;
$b = new stdClass;
$b->p = 1;
$s[$a] = 'A';
$s->attach($b);
$s->attach($a, 'A2');
var_dump($s);
print_r($s);
echo "\n";
var_dump(count($s), $s->count(), $s[$a], $s[$b], isset($s[$a]), $s->contains($b));
t(fn() => $s[new stdClass]);
t(fn() => $s['x']);
t(fn() => $s->attach('x'));
t(function () use ($s) { $s[] = 1; });
t(fn() => $s->count(COUNT_NORMAL));
foreach ($s as $k => $v) { var_dump($k, $v === $a || $v === $b, $s->getInfo()); $s->setInfo("i$k"); }
var_dump($s[$a], $s[$b]);
$s->rewind();
var_dump($s->valid(), $s->key(), $s->current() === $a);
$s->next();
$s->next();
var_dump($s->valid(), $s->key());
t(fn() => $s->current());
var_dump($s->getInfo());
unset($s[$a]);
var_dump(count($s));
$s->detach($b);
var_dump(count($s));
$s->attach($a);
$s2 = new SplObjectStorage;
$s2->attach($b, 'x');
$s2->attach($a, 'y');
var_dump($s->addAll($s2), count($s), $s[$a]);
var_dump($s->removeAll($s2), count($s));
t(fn() => $s->addAll(new stdClass));
var_dump(serialize($s2));
$u = unserialize(serialize([$a, $s2]));
print_r($u);
var_dump($u[1][$u[0]]);
$c = clone $s2;
$c->detach($a);
var_dump(count($s2), count($c));
var_dump($s2->getHash($a) === spl_object_hash($a), $s2->__serialize(), (array)$s2, json_encode($s2));
t(fn() => $s2->seek(1));
var_dump($s2->current() === $a);
t(fn() => $s2->seek(5));
var_dump($s2->removeAllExcept($s), count($s2));
$e = new SplObjectStorage;
$e[$a] = 5;
$e[$a]++;
var_dump($e[$a]);
$e[$a] .= 'x';
var_dump($e[$a]);
$n = new SplObjectStorage;
$n[$a] = null;
var_dump(isset($n[$a]), empty($n[$a]));
t(fn() => $n->__unserialize([[1, 2], []]));
t(fn() => $n->__unserialize([[$a], []]));
t(fn() => $n->__unserialize([]));

enum Foo { case Bar; case Baz; case Qux; }
$storage = new SplObjectStorage();
$storage[Foo::Bar] = 'Bar';
$storage[Foo::Baz] = 'Baz';
var_dump($storage->contains(Foo::Qux), serialize($storage));
$back = unserialize(serialize($storage));
var_dump($back[Foo::Bar], $back[Foo::Baz]);
list(Foo::Bar => $bar, Foo::Baz => $baz) = $storage;
var_dump($bar, $baz);
