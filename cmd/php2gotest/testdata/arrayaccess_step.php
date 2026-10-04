<?php
// `++` and `--` on an ArrayAccess element fetch it for writing: offsetGet
// runs and offsetSet never does. An `&offsetGet` hands back the box the step
// goes through -- a typed property's, overflow refusal included -- while a
// by-value offsetGet earns `Indirect modification of overloaded element`
// and changes nothing. A compound assignment still reads and calls
// offsetSet (typed_properties_065).

class V implements ArrayAccess {
    public $v = 5;
    function offsetExists($o): bool { return true; }
    function offsetGet($o): mixed { echo "get($o)\n"; return $this->v; }
    function offsetSet($o, $x): void { echo "set($o, $x)\n"; }
    function offsetUnset($o): void {}
}
class R implements ArrayAccess {
    public int $v = 5;
    function offsetExists($o): bool { return true; }
    function &offsetGet($o): mixed { echo "rget($o)\n"; return $this->v; }
    function offsetSet($o, $x): void { echo "rset($o, $x)\n"; }
    function offsetUnset($o): void {}
}

$a = new V;
var_dump($a[0]++, $a->v);
var_dump(--$a['k'], $a->v);
$a[1] += 2;

$r = new R;
var_dump($r[0]++, $r->v);
var_dump(--$r[0], $r->v);
$r[0] .= "1";
var_dump($r->v);
$r->v = PHP_INT_MAX;
try {
    $r[0]++;
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
var_dump($r->v);
