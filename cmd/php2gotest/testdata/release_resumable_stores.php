<?php
// The store claims in a file body the split made resumable (`inner()` at the
// top level may suspend): a bare `new` statement, an element or property
// overwrite, and a reference rebind each destroy what they drop, on either
// side of the split. Negative: a container something else reads keeps its
// element.
class D {
    function __construct(public $n) {}
    function __destruct() { echo "D{$this->n} gone\n"; }
}
function inner() { return Fiber::getCurrent() ? Fiber::suspend('s') : 'outside'; }
new D(1);
echo "after discarded\n";
echo inner(), "\n";
new D(2);
echo "after discarded past the split\n";
$o = new stdClass;
$o->p = new D(3);
echo inner(), "\n";
$o->p = 1;
echo "after property overwrite\n";
$a = [new D(4)];
$a[0] = 2;
echo "after element overwrite\n";
$r = new D(5);
$x = 1;
$r = &$x;
echo "after rebind\n";
$shared = [new D(6)];
$copy = $shared;
$shared[0] = 3;
echo "after overwrite of a copied array\n";
echo "end\n";
