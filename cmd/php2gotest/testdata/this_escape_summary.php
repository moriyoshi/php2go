<?php
// Which methods let their receiver outlive the call (lower.LeaksThisWith).
// Each case calls methods on a local, unsets it, and shows whether the
// object died there: the next object takes its number, or it lives on and
// the number is not reused.

// Positive: every method only inspects $this, and the destructor runs at
// the unset.
class Inspect implements Countable {
    public $n = 1;
    function name() { return get_class($this) . ":" . spl_object_id($this); }
    function same() { return $this === null; }
    function isSelf($o) { return $this === $o || $this !== $o; }
    function kind() { return $this instanceof Countable ? "countable" : "plain"; }
    function show() { var_dump($this); print_r($this); echo "\n"; }
    function say() { echo $this, "\n"; }
    function __toString(): string { return "Inspect(" . $this->n . ")"; }
    function count(): int { return 3; }
    function counted() { return count($this); }
    function chain() { return $this->helper() + $this->helper(); }
    function helper() { return $this->n; }
    function copy() { $c = clone $this; $c->n = 9; return $c->n; }
    function types() { return gettype($this) . "/" . get_debug_type($this) . "/" . (is_object($this) ? "o" : "-"); }
    function __destruct() { echo "~Inspect ", get_class($this), " ", $this->n, "\n"; }
}

function inspect() {
    $o = new Inspect;
    echo $o->name() === "Inspect:" . spl_object_id($o) ? "name ok\n" : "name bad\n";
    var_dump($o->same(), $o->isSelf(null), $o->kind(), $o->counted(), $o->chain(), $o->copy(), $o->types());
    $o->show();
    $o->say();
    $id = spl_object_id($o);
    unset($o);
    echo "after unset\n";
    $n = new stdClass;
    var_dump(spl_object_id($n) === $id);
}
inspect();

// Negative: each class has one method that lets $this out; after it is
// called the object must survive the unset, so its number is not reused.
class Holder { static $h = []; }
function keep($x) { Holder::$h[] = $x; }

class StoresStatic { function storeStatic() { Holder::$h[] = $this; } }
class ViaMethod { function viaMethod() { $this->inner(); } function inner() { Holder::$h[] = $this; } }
class Base { function baseKeep() { Holder::$h[] = $this; } }
class ViaParent extends Base { function viaParent() { parent::baseKeep(); } }
class ViaFunction { function viaFunction() { keep($this); } }
class ViaClosure { function viaClosure() { Holder::$h[] = function () { return $this; }; } }
class ViaCallable { function viaCallable() { Holder::$h[] = [$this, 'viaCallable']; } }
class ViaFcc { function viaFcc() { Holder::$h[] = $this->viaFcc(...); } }
class ViaGenerator { function viaGenerator() { yield 1; } }
class ViaArray { function viaArray() { Holder::$h[] = [$this]; } }
class ViaProperty { public $self; function viaProperty() { $this->self = $this; } }
// An engine parent's method this unit has no body for: ArrayObject's
// getIterator hands back an iterator that holds the object.
class ViaEngine extends ArrayObject { function viaEngine() { Holder::$h[] = $this->getIterator(); } }
class Returns { function returnsThis() { return $this; } }

function negStoresStatic() {
    $a = new StoresStatic;
    $a->storeStatic();
    $id = spl_object_id($a);
    unset($a);
    echo "StoresStatic ";
    var_dump(spl_object_id(new stdClass) === $id);
}
function negViaMethod() {
    $a = new ViaMethod;
    $a->viaMethod();
    $id = spl_object_id($a);
    unset($a);
    echo "ViaMethod ";
    var_dump(spl_object_id(new stdClass) === $id);
}
function negViaParent() {
    $a = new ViaParent;
    $a->viaParent();
    $id = spl_object_id($a);
    unset($a);
    echo "ViaParent ";
    var_dump(spl_object_id(new stdClass) === $id);
}
function negViaFunction() {
    $a = new ViaFunction;
    $a->viaFunction();
    $id = spl_object_id($a);
    unset($a);
    echo "ViaFunction ";
    var_dump(spl_object_id(new stdClass) === $id);
}
function negViaClosure() {
    $a = new ViaClosure;
    $a->viaClosure();
    $id = spl_object_id($a);
    unset($a);
    echo "ViaClosure ";
    var_dump(spl_object_id(new stdClass) === $id);
}
function negViaCallable() {
    $a = new ViaCallable;
    $a->viaCallable();
    $id = spl_object_id($a);
    unset($a);
    echo "ViaCallable ";
    var_dump(spl_object_id(new stdClass) === $id);
}
function negViaFcc() {
    $a = new ViaFcc;
    $a->viaFcc();
    $id = spl_object_id($a);
    unset($a);
    echo "ViaFcc ";
    var_dump(spl_object_id(new stdClass) === $id);
}
function negViaGenerator() {
    $a = new ViaGenerator;
    $g = $a->viaGenerator();
    $id = spl_object_id($a);
    unset($a);
    echo "ViaGenerator ";
    var_dump(spl_object_id(new stdClass) === $id);
    Holder::$h[] = $g;
}
function negViaArray() {
    $a = new ViaArray;
    $a->viaArray();
    $id = spl_object_id($a);
    unset($a);
    echo "ViaArray ";
    var_dump(spl_object_id(new stdClass) === $id);
}
function negViaProperty() {
    $a = new ViaProperty;
    $a->viaProperty();
    $id = spl_object_id($a);
    unset($a);
    echo "ViaProperty ";
    var_dump(spl_object_id(new stdClass) === $id);
}
function negReturns() {
    $a = new Returns;
    $kept = $a->returnsThis();
    $id = spl_object_id($a);
    unset($a);
    echo "Returns ";
    var_dump(spl_object_id(new stdClass) === $id, $kept instanceof Returns);
}
negStoresStatic();
negViaMethod();
negViaParent();
negViaFunction();
negViaClosure();
negViaCallable();
negViaFcc();
negViaGenerator();
negViaArray();
negViaProperty();
negReturns();
function negViaEngine() {
    $a = new ViaEngine;
    $a->viaEngine();
    $id = spl_object_id($a);
    unset($a);
    echo "ViaEngine ";
    var_dump(spl_object_id(new stdClass) === $id);
}
negViaEngine();

// A generator method's Generator keeps its frame, and so $this, alive: the
// unset must not destroy the object while the generator is live.
class Gen { function items() { yield 1; } function __destruct() { echo "~Gen\n"; } }
function genHolds() {
    $a = new Gen;
    $g = $a->items();
    unset($a);
    echo "Gen after unset\n";
    return $g;
}
$g = genHolds();
echo "item ", $g->current(), "\n";
echo "done\n";
