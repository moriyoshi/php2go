<?php
class PV {
    private $m = 1 { get => $this->m + 41; set { $this->m = $value * 2; } }
    public function pm() { return $this->m; }
    public function setm($v) { $this->m = $v; }
    public function has() { return isset($this->m); }
}
class PW extends PV { public $m = 5; }
$w = new PW; echo "PW ", $w->m, " ", $w->pm(), "\n";
$w->setm(10); echo "after setm ", $w->m, " ", $w->pm(), "\n";
$w->m = 7; echo "after m=7 ", $w->m, " ", $w->pm(), "\n";
var_dump($w->has(), isset($w->m));
// child with no redeclaration: PV's private is invisible from outside
class PX extends PV {}
$x = new PX; echo "PX ", $x->pm(), "\n";
$x->setm(3); echo "PX setm ", $x->pm(), "\n";
echo isset($x->m) ? "PX m set\n" : "PX m unset\n";
// child redeclares its own hooked public
class PY extends PV { public $m = 100 { get => $this->m + 1; } }
$y = new PY; echo "PY ", $y->m, " ", $y->pm(), "\n";
// child redeclares private, both hooked
class PZ extends PV { private $m = 50 { get => $this->m + 2; } public function zm() { return $this->m; } }
$z = new PZ; echo "PZ ", $z->zm(), " ", $z->pm(), "\n";
// protected parent hook shadowed by public child plain -> child redeclaration replaces hook? 
class Q1 { protected $n = 1 { get => $this->n * 10; } public function qn() { return $this->n; } }
class Q2 extends Q1 { public $n = 2; }
$q = new Q2; echo "Q2 ", $q->n, " ", $q->qn(), "\n";
// from the declaring scope on each shape of object
class PV2 {
    private $m = 1 { get => $this->m + 41; set { $this->m = $value * 2; } }
    public function un() { try { unset($this->m); echo "unset ok\n"; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } }
    public function ref() { $x = 3; try { $this->m = &$x; echo "ref ok\n"; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } }
    public function iss() { var_dump(isset($this->m), empty($this->m)); }
    public function arr() { try { $this->m[] = 1; echo "arr ok\n"; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } }
}
class PX2 extends PV2 {}
class PW2 extends PV2 { public $m = 5; }
foreach ([new PV2, new PX2, new PW2] as $o) { echo get_class($o), "\n"; $o->iss(); $o->un(); $o->ref(); $o->arr(); }
