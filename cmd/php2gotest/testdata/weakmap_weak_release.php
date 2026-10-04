<?php
// A weak container lets go of an object when its last strong holder does,
// and a WeakMap's dimensions, iteration and clone behave as the engine's.

echo "-- WeakReference cleared at unset\n";
$std = new stdClass;
debug_zval_dump($std);
$wr = WeakReference::create($std);
debug_zval_dump($wr->get());
unset($std);
var_dump($wr->get());
var_dump($wr);

echo "-- WeakMap entry dropped at unset and overwrite\n";
$map = new WeakMap;
$a = new stdClass;
$b = new stdClass;
$map[$a] = 1;
$map[$b] = [1];
var_dump(isset($map[$a]), $map[$a], count($map));
unset($a);
var_dump(count($map), $map->count());
$b = null;
var_dump($map);
$n = new stdClass;
var_dump(spl_object_id($n));

echo "-- a cloned map lets go too\n";
$m1 = new WeakMap;
$k = new stdClass;
$m1[$k] = "v";
$m2 = clone $m1;
unset($k);
var_dump(count($m1), count($m2));
unset($m1, $m2);
$z = new stdClass;
var_dump(spl_object_id($z));

echo "-- comparisons and inspection keep nothing\n";
$c = new stdClass;
$cw = WeakReference::create($c);
var_dump($cw === WeakReference::create($c), $cw->get() === $c);
unset($c);
var_dump($cw->get());

echo "-- a target handed back and kept stays\n";
$o = new stdClass;
$ow = WeakReference::create($o);
$keep = $ow->get();
unset($o);
var_dump($ow->get() === $keep);

echo "-- a key reached through iteration and kept stays\n";
$im = new WeakMap;
$q = new stdClass;
$im[$q] = 1;
foreach ($im as $key => $v) {
    $held = $key;
}
unset($q);
var_dump(count($im), $held === $key);

echo "-- a key stored as a value or a property stays\n";
$vm = new WeakMap;
$s = new stdClass;
$vm[$s] = $s;
unset($s);
var_dump(count($vm));
$pm = new WeakMap;
$box = new stdClass;
$t = new stdClass;
$box->t = $t;
$pm[$t] = 1;
unset($t);
var_dump(count($pm));

echo "-- dimensions write through the entry\n";
$dm = new WeakMap;
$e = new stdClass;
$dm[$e] = 1;
$dm[$e]++;
$dm[$e] .= "x";
var_dump($dm[$e]);
$dm[$e] = [1];
$dm[$e][] = 2;
var_dump($dm[$e]);
$x = 1;
$dm[$e] =& $x;
$x = 5;
var_dump($dm);

echo "-- iteration\n";
$it = new WeakMap;
$i0 = new stdClass;
$i1 = new stdClass;
$i2 = new stdClass;
$it[$i0] = 0;
$it[$i1] = 1;
$it[$i2] = 2;
foreach ($it as $key => $value) {
    if ($key === $i0) {
        unset($it[$i1]);
    }
    var_dump($key, $value);
}
foreach ($it as $key => &$value) {
    $value += 10;
}
unset($value);
var_dump($it);
foreach ($it as $key => $value) {
    if ($key === $i0) {
        unset($it[$i0]);
    }
    echo $value, "\n";
}
var_dump(count($it));
$sm = new WeakMap;
$sa = new stdClass;
$sb = new stdClass;
$sc = new stdClass;
$sd = new stdClass;
$sm[$sa] = "a";
$sm[$sb] = "b";
$sm[$sc] = "c";
$sm[$sd] = "d";
foreach ($sm as $key => $value) {
    if ($key === $sc) {
        unset($sm[$sa]);
    }
    echo $value, "\n";
}
var_dump($it instanceof IteratorAggregate, $it instanceof Traversable);

echo "-- clones are independent\n";
$cm = new WeakMap;
$ca = new stdClass;
$cb = new stdClass;
$cm[$ca] = 1;
$cc = clone $cm;
$cc[$cb] = 2;
$cc[$ca] = 3;
unset($cm[$ca]);
var_dump(count($cm), count($cc), $cc[$ca]);

echo "-- refusals\n";
$missing = new stdClass;
try {
    $dm[$missing]++;
} catch (Error $err) {
    echo get_class($err), ": ", $err->getMessage(), "\n";
}
try {
    $dm[$missing] =& $x;
} catch (Error $err) {
    echo get_class($err), ": ", $err->getMessage(), "\n";
}
try {
    $dm[] =& $x;
} catch (Error $err) {
    echo get_class($err), ": ", $err->getMessage(), "\n";
}
try {
    $dm[1] =& $x;
} catch (Error $err) {
    echo get_class($err), ": ", $err->getMessage(), "\n";
}

echo "-- an ArrayAccess refuses a reference\n";
class AA implements ArrayAccess {
    function offsetGet($k): mixed { return 1; }
    function offsetSet($k, $v): void {}
    function offsetExists($k): bool { return true; }
    function offsetUnset($k): void {}
}
$aa = new AA;
try {
    $aa[1] =& $x;
} catch (Error $err) {
    echo get_class($err), ": ", $err->getMessage(), "\n";
}

echo "-- a destructor runs where the last holder lets go\n";
class D {
    function __destruct() { echo "D dtor\n"; }
}
$dmap = new WeakMap;
$dd = new D;
$dmap[$dd] = 1;
$dw = WeakReference::create($dd);
unset($dd);
var_dump(count($dmap), $dw->get() === null);

echo "-- a destructor that keeps \$this keeps the entry\n";
class R {
    function __destruct() {
        global $saved;
        echo "R dtor\n";
        $saved = $this;
    }
}
$rmap = new WeakMap;
$rr = new R;
$rmap[$rr] = 1;
$rw = WeakReference::create($rr);
unset($rr);
var_dump(count($rmap), $rw->get() !== null);

echo "-- a map another body can rebind is not trusted\n";
class K {
    function __destruct() { echo "K dtor\n"; }
}
class Store implements ArrayAccess {
    public $keys = [];
    function offsetGet($k): mixed { return 1; }
    function offsetSet($k, $v): void { $this->keys[] = $k; }
    function offsetExists($k): bool { return true; }
    function offsetUnset($k): void {}
}
function rebind() {
    global $gm;
    $gm = new Store;
}
$gm = new WeakMap;
rebind();
$gk = new K;
$gm[$gk] = 1;
unset($gk);
echo "after unset\n";
