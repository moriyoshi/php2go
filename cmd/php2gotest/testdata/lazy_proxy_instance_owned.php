<?php
// An initialized lazy proxy has no destructor of its own: the instance's runs
// when the instance dies. When the initializer made the instance and kept no
// copy, and nothing running on the instance keeps `$this`, the proxy is its
// only holder -- the instance dies with the proxy, its destructor runs there
// with the instance as `$this`, and its number goes back before the proxy's.
class C {
    public $a = 1;
    public function __destruct() { echo "C::__destruct #", spl_object_id($this), "\n"; }
}

class D { public $a = 1; }
// A constructor that keeps `$this`: the instance is not the proxy's alone,
// though nothing else about the proxy shows it.
class CtorKeeps {
    public $a = 1;
    public function __construct() { Holder::$byCtor = $this; }
}
class S {
    public $a = 1;
    // What `$this` is, without its number: the instance, never the proxy.
    public function __destruct() {
        ob_start();
        var_dump($this);
        echo "S::__destruct on ", preg_replace('/#\d+/', '#', ob_get_clean());
    }
}
class Holder { public static $held; public static $byCtor; }
class Leaky {
    public $a = 1;
    public function grab() { Holder::$held = $this; }
}

$r = new ReflectionClass(C::class);
$p = $r->newLazyProxy(function ($o) { return new C(); });
echo "proxy ", spl_object_id($p), "\n";
var_dump($p->a);
$p = null;
echo "after proxy dropped\n";
$x = new stdClass; $y = new stdClass; $z = new stdClass;
echo spl_object_id($x), " ", spl_object_id($y), " ", spl_object_id($z), "\n";
unset($x, $y, $z);

// A proxy local to a function takes its instance with it when the frame ends.
function local() {
    $r = new ReflectionClass(C::class);
    $p = $r->newLazyProxy(function ($o) { return new C(); });
    $p->a = 5;
    echo "local proxy ", spl_object_id($p), "\n";
}
local();
echo "after local\n";

// The initializer keeps a copy: the instance outlives the proxy.
$kept = null;
$dr = new ReflectionClass(D::class);
$q = $dr->newLazyProxy(function ($o) use (&$kept) { $kept = new D(); return $kept; });
var_dump($q->a);
$q = null;
$n = new stdClass;
echo "kept instance: ", $kept->a, ", its number ", spl_object_id($n) === spl_object_id($kept) ? "reused" : "kept", "\n";
unset($n);

// A method keeps `$this`: the instance is no longer the proxy's alone.
$lr = new ReflectionClass(Leaky::class);
$l = $lr->newLazyProxy(function ($o) { return new Leaky(); });
$l->grab();
$l = null;
$n = new stdClass;
echo "held instance: ", Holder::$held->a, ", its number ", spl_object_id($n) === spl_object_id(Holder::$held) ? "reused" : "kept", "\n";
unset($n);

function ctorKeeps() {
    $p = (new ReflectionClass(CtorKeeps::class))->newLazyProxy(function ($o) { return new CtorKeeps(); });
    var_dump($p->a);
}
ctorKeeps();
$n = new stdClass;
echo "ctor-kept instance: ", Holder::$byCtor->a, ", its number ", spl_object_id($n) === spl_object_id(Holder::$byCtor) ? "reused" : "kept", "\n";
unset($n);

// A proxy still alive when the script ends: only its instance's destructor
// runs, once.
$survivor = (new ReflectionClass(S::class))->newLazyProxy(function ($o) { return new S(); });
var_dump($survivor->a);
echo "end\n";
