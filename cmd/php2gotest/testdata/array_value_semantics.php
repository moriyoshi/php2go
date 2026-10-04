<?php
// Arrays are values. Compiled output defers the copy an assignment makes --
// the new holder shares the elements until one of them writes (OWNERSHIP-PERF.md
// P3b) -- and every shape here is one where a missed separation, or a copy that
// is not made at all, would let one holder see another's write.

// A parameter is the callee's own, even when the caller's original changes
// under it -- through a global or a property.
$g = [1];
function viaGlobal($a)
{
    global $g;
    $g[] = 2;
    return count($a);
}

class Holder
{
    public $p = [1];
    public function viaProperty($a)
    {
        $this->p[] = 2;
        return count($a);
    }
    public function items()
    {
        return $this->p;
    }
    public function add($v)
    {
        $this->p[] = $v;
    }
}

// A promoted copy: the store that copied is gone, the copy must not be.
function promotedCopy()
{
    $a = [1];
    $b = $a;
    $a[] = 2;
    return [count($a), count($b)];
}

// An element read out and then written through its container.
function elementCopy()
{
    $a = [[1]];
    $b = $a[0];
    $a[0][] = 2;
    return [count($a[0]), count($b)];
}

// A property's array handed out by a method, then grown by another.
function methodResult()
{
    $h = new Holder();
    $items = $h->items();
    $h->add(9);
    return [count($items), count($h->p)];
}

// foreach by value binds a copy of each element.
function foreachCopies()
{
    $a = [[1], [2]];
    $seen = [];
    foreach ($a as $i => $v) {
        $a[$i][] = 0;
        $seen[] = count($v);
    }
    return [$seen, count($a[0])];
}

// References are decided when the arrays separate, as PHP does: one still held
// by a name survives, one only the array holds is flattened into the writer's
// copy.
function referencesAtSeparation()
{
    $a = [1, 2];
    $r = &$a[0];
    $b = $a;
    unset($r);
    $a[0] = 7;
    $flattened = $b[0];

    $c = [1, 2];
    $s = &$c[0];
    $d = $c;
    $c[0] = 7;
    return [$flattened, $d[0]];
}

// Shared, then written on both sides, several times.
function bothSides($n)
{
    $base = [0];
    $copies = [];
    for ($i = 0; $i < $n; $i++) {
        $x = $base;
        $x[] = $i;
        $copies[] = $x;
        $base[] = -$i;
    }
    return [$base, $copies];
}

// A nested array written after its container was shared.
function nestedAfterShare()
{
    $a = ["k" => [1, 2]];
    $b = $a;
    $a["k"][] = 3;
    $b["k"][0] = 9;
    return [$a, $b];
}

// The internal pointer belongs to each holder.
function pointers()
{
    $a = [1, 2, 3];
    next($a);
    $b = $a;
    next($b);
    return [current($a), current($b)];
}

// A static property and a local sharing one array.
class Store
{
    public static $s = [];
}

function staticShare()
{
    $local = [1];
    Store::$s = $local;
    $local[] = 2;
    Store::$s[] = 3;
    return [count($local), count(Store::$s)];
}

var_dump(viaGlobal($g), count($g));
$h = new Holder();
var_dump($h->viaProperty($h->p), count($h->p));
var_dump(promotedCopy());
var_dump(elementCopy());
var_dump(methodResult());
var_dump(foreachCopies());
var_dump(referencesAtSeparation());
var_dump(bothSides(3));
var_dump(nestedAfterShare());
var_dump(pointers());
var_dump(staticShare());
