<?php
// Compiled code reads and writes a public, unhooked, declared property without
// asking the questions the full path asks (OWNERSHIP-PERF.md, the property fast
// path). Each shape here is one where skipping a question would show: a
// declaration that refuses, a hook, magic that takes over once the slot is
// gone, a private the public name shadows, a lazy object, a reference whose box
// carries a type, and the step at the edge of the integer range.

class Plain
{
    public $p = 1;
    public $a = [1];
    protected $prot = 2;
    private $priv = 3;
}

class Typed
{
    public int $i = 1;
    public $u = null;
    public ?string $s = null;
}

class Once
{
    public function __construct(public readonly int $v)
    {
    }
}

class Asym
{
    public private(set) int $w = 1;

    public function set($v)
    {
        $this->w = $v;
    }
}

class Hooked
{
    public $plain = "plain";
    public $h = 0 {
        set => $value * 10;
        get => $this->h + 1;
    }
}

class HookedChild extends Hooked
{
    public $own = "own";
}

class Magic
{
    public $m = "declared";

    public function __get($n)
    {
        return "__get($n)";
    }

    public function __set($n, $v)
    {
        echo "__set($n)\n";
    }
}

class Base
{
    private $s = "base";

    public function baseS()
    {
        return $this->s;
    }

    public function setBaseS($v)
    {
        $this->s = $v;
    }
}

class Derived extends Base
{
    public $s = "derived";
}

class Counter
{
    public $n = 0;
    public int $t = PHP_INT_MAX;
}

function attempt(callable $f)
{
    try {
        $f();
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

// The plain case, many times over, and a clone that must not share a write.
$o = new Plain();
for ($k = 0; $k < 5; $k++) {
    $o->p++;
    $o->p = $o->p * 2;
}
$c = clone $o;
$c->p = "clone";
echo $o->p, " ", $c->p, "\n";

// An array property is a value: storing one and appending to it leaves the
// source alone, both ways round.
$arr = [1, 2];
$o->a = $arr;
$o->a[] = 3;
$arr[] = 4;
echo count($o->a), " ", count($arr), "\n";

// A reference into a plain slot: the write goes through the box.
$r = &$o->p;
$o->p = "through";
echo $r, "\n";
$r = "back";
echo $o->p, "\n";
unset($r);

// Unset, then read and write: the read warns, the write recreates silently.
unset($o->p);
var_dump(isset($o->p));
echo @$o->p === null ? "null\n" : "set\n";
$o->p = "again";
echo $o->p, "\n";

// A typed property still coerces, and refuses.
$t = new Typed();
$t->i = "7";
var_dump($t->i);
attempt(function () use ($t) { $t->i = "no"; });
$t->s = 5;
var_dump($t->s);

// An untyped slot holding the typed property's box refuses as the typed one.
$t->u = &$t->i;
attempt(function () use ($t) { $t->u = "e50x"; });
$t->u = "8";
var_dump($t->i);

// A readonly property: once, and never again.
$once = new Once(3);
echo $once->v, "\n";
attempt(function () use ($once) { $once->v = 4; });

// Asymmetric visibility: readable, not writable from outside.
$asym = new Asym();
echo $asym->w, "\n";
attempt(function () use ($asym) { $asym->w = 2; });
$asym->set(5);
echo $asym->w, "\n";

// A class with a hook: its plain neighbour too goes the long way, and the hook
// runs for its own property -- also on a subclass that adds none.
$h = new Hooked();
$h->h = 2;
echo $h->h, " ", $h->plain, "\n";
$h->plain = "changed";
echo $h->plain, "\n";
$hc = new HookedChild();
$hc->h = 3;
echo $hc->h, " ", $hc->own, "\n";

// Magic takes over only once the declared slot is gone.
$m = new Magic();
echo $m->m, "\n";
$m->m = "written";
echo $m->m, "\n";
unset($m->m);
echo $m->m, "\n";
$m->m = "via set";
echo $m->m, "\n";

// A public name shadowing an ancestor's private: two slots, and each scope
// reaches its own.
$d = new Derived();
echo $d->s, " ", $d->baseS(), "\n";
$d->s = "D";
$d->setBaseS("B");
echo $d->s, " ", $d->baseS(), "\n";

// A lazy ghost initialises on the first write and on the first read.
$rc = new ReflectionClass(Plain::class);
$ghost = $rc->newLazyGhost(function ($g) {
    echo "init\n";
    $g->p = "from init";
});
echo "before\n";
$ghost->p = "written";
echo $ghost->p, "\n";
$ghost2 = $rc->newLazyGhost(function ($g) {
    echo "init2\n";
});
echo $ghost2->p, "\n";

// A lazy proxy holds no state of its own: once initialised, every read and
// write lands on the instance behind it, however often it is touched.
$real = null;
$proxy = $rc->newLazyProxy(function ($p) use (&$real) {
    echo "proxy init\n";
    $real = new Plain();
    $real->p = "real";
    return $real;
});
echo $proxy->p, "\n";
$proxy->p = "via proxy";
echo $real->p, " ", $proxy->p, "\n";
$real->p = "via real";
echo $proxy->p, "\n";
$proxy2 = $rc->newLazyProxy(function ($p) {
    echo "proxy2 init\n";
    $r = new Plain();
    $r->p = "r2";
    return $r;
});
$proxy2->p = "first write";
echo $proxy2->p, "\n";

// A dynamic property is no declaration: the deprecation still fires, once.
$dyn = new Plain();
$dyn->extra = 1;
$dyn->extra = 2;
echo $dyn->extra, "\n";

// The step at the edge: an untyped property widens, an int one refuses.
$cn = new Counter();
$cn->n = PHP_INT_MAX;
$cn->n++;
var_dump($cn->n);
$cn->n = PHP_INT_MIN;
$cn->n--;
var_dump($cn->n);
attempt(function () use ($cn) { $cn->t++; });
var_dump($cn->t);
$i = PHP_INT_MAX;
$i++;
var_dump($i);
$j = PHP_INT_MIN;
$j--;
var_dump($j);
var_dump(PHP_INT_MAX - 1 < PHP_INT_MAX, 3 <= 3, 4 <= 3, -1 < 0);

// Last, because a caught Error does not yet give its handle number back in
// compiled output.
attempt(function () use ($o) { return $o->prot; });
attempt(function () use ($o) { return $o->priv; });
attempt(function () use ($o) { $o->prot = 1; });
echo "end\n";
