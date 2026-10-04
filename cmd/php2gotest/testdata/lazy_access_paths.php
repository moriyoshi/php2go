<?php
// Access paths that reach a lazy object's state, each probed: which of them
// initialise, which ask a magic accessor on the stand-in first, and which act
// on a proxy's instance rather than the proxy. print_r rather than var_dump,
// so that object numbers stay out of it.

class C {
    public int $a;
    public int $b;
    public function __construct() {
        echo "C::__construct\n";
        $this->a = 1;
        $this->b = 2;
    }
}
$rc = new ReflectionClass(C::class);
$ghost = fn() => $rc->newLazyGhost(function ($o) { echo "init ghost\n"; $o->__construct(); });
$proxy = fn() => $rc->newLazyProxy(function () { echo "init proxy\n"; return new C; });

echo "# foreach by reference initialises, and binds the instance's slots\n";
foreach ([$ghost, $proxy] as $make) {
    $o = $make();
    foreach ($o as $k => &$v) {
        echo "$k=$v\n";
        $v *= 10;
    }
    unset($v);
    echo $o->a, " ", $o->b, "\n";
}

echo "# unset on an initialized proxy removes from the instance\n";
class U { public $a = 1; public int $b = 1; }
$ru = new ReflectionClass(U::class);
$u = $ru->newLazyProxy(function () { echo "init U\n"; return new U; });
unset($u->a);
print_r($ru->initializeLazyObject($u));
var_dump(isset($u->a));

echo "# array_walk initialises and walks the instance\n";
$o = $proxy();
array_walk($o, function (&$v, $k) { $v += 100; });
echo $o->a, " ", $o->b, "\n";

echo "# array_splice converts an object replacement as (array) does\n";
class P { public $x = 1; protected $y = 2; private $z = 3; public $w; }
$a = [9];
array_splice($a, 1, 0, new P);
print_r($a);
$a = [1, 2];
array_splice($a, 0, 1, null);
print_r($a);
$a = [1];
array_splice($a, 0, 0, 'scalar');
print_r($a);
$l = $rc->newLazyGhost(function () { echo "never\n"; });
$rc->getProperty('a')->setRawValueWithoutLazyInitialization($l, 3);
$a = [];
array_splice($a, 0, 0, $l);
print_r($a);
var_dump($rc->isUninitializedLazyObject($l));

echo "# isset asks __isset on the stand-in first, __get never initialises\n";
class I { public $_; function __isset($n) { echo "I::__isset $n\n"; return true; } }
class G { public $_; function __get($n) { echo "G::__get $n\n"; return [1]; } }
class B { public $_; function __isset($n) { echo "B::__isset $n\n"; return true; } function __get($n) { echo "B::__get $n\n"; return [1]; } }
class N { public $_; }
class R {
    public $_;
    function __isset($n) { echo "R::__isset $n\n"; return isset($this->$n['']); }
    function __get($n) { echo "R::__get $n\n"; return $this->$n['']; }
}
foreach (['I', 'G', 'B', 'N', 'R'] as $cls) {
    echo "== $cls\n";
    $r = new ReflectionClass($cls);
    $p = $r->newLazyProxy(function () use ($cls) { echo "init $cls\n"; return new $cls; });
    var_dump(@isset($p->prop[0]));
    var_dump($r->isUninitializedLazyObject($p));
    $p = $r->newLazyProxy(function () use ($cls) { echo "init $cls\n"; return new $cls; });
    var_dump(@($p->other ?? 'dflt'));
}

echo "# a by-reference fetch reaches &__get on the stand-in\n";
class RG { public $_; public function &__get($n) { echo "RG::__get $n\n"; return $n; } }
$p = (new ReflectionClass(RG::class))->newLazyProxy(function () { echo "init RG\n"; return new RG; });
$x = &$p->x;
var_dump($x);

echo "# getValue/setValue/isInitialized, per property\n";
class V { public int $id; public string $t; public $u; }
$rv = new ReflectionClass(V::class);
$pid = $rv->getProperty('id');
$pt = $rv->getProperty('t');
foreach (['newLazyGhost', 'newLazyProxy'] as $m) {
    foreach (['getValue', 'setValue', 'isInitialized'] as $op) {
        $mk = $m == 'newLazyGhost'
            ? function ($o) { echo "init\n"; $o->t = "T"; }
            : function () { echo "init\n"; $n = new V; $n->t = "T"; return $n; };
        $o = $rv->$m($mk);
        $pid->skipLazyInitialization($o);
        echo "$m $op skipped: ";
        try {
            var_dump($op == 'setValue' ? $pid->setValue($o, 5) : $pid->$op($o));
        } catch (Error $e) {
            echo get_class($e), ": ", $e->getMessage(), "\n";
        }
        var_dump($rv->isUninitializedLazyObject($o));
        echo "$m $op lazy: ";
        var_dump($op == 'setValue' ? $pt->setValue($o, "x") : $pt->$op($o));
        var_dump($rv->isUninitializedLazyObject($o), $o->t);
    }
}

echo "# skip and raw writes on an initialized proxy act on its instance\n";
class K { public $a = 1; public $b; public function __construct() { $this->a = 2; } }
$rk = new ReflectionClass(K::class);
$k = $rk->newLazyProxy(function () { return new K; });
$rk->initializeLazyObject($k);
$rk->getProperty('a')->skipLazyInitialization($k);
var_dump($k->a);
$rk->getProperty('b')->setRawValueWithoutLazyInitialization($k, 'raw');
print_r($rk->initializeLazyObject($k));

echo "# binding a missing property by reference creates it: deprecation first\n";
class A { public $_; }
#[AllowDynamicProperties] class AD {}
readonly class RO { public int $a; function __construct() { $this->a = 1; } }
enum En { case X; }
$val = 1;
$plain = new A;
$plain->x =& $val;
$plain->x =& $val;
$ad = new AD;
$ad->x =& $val;
$ro = new RO;
try {
    $ro->x =& $val;
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    $en = En::X;
    $en->x =& $val;
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
$ra = new ReflectionClass(A::class);
$lp = $ra->newLazyProxy(fn() => new A);
$ra->initializeLazyObject($lp);
$lp->y =& $val;
$lg = $ra->newLazyGhost(function () {});
$lg->z =& $val;
print_r($ra->initializeLazyObject($lp));
print_r($lg);
set_error_handler(function ($no, $str) { throw new Exception($str); });
$thrown = new A;
try {
    $thrown->w =& $val;
} catch (Exception $e) {
    echo "caught: ", $e->getMessage(), "\n";
}
restore_error_handler();
print_r($thrown);
