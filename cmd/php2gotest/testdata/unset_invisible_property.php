<?php
// unset() of a property the calling scope cannot see goes to __unset when
// there is one to ask; when there is none -- or its guard is already up,
// because the unset is reached from inside __unset -- it is refused, and
// the property stays.

class Plain { private $priv = 1; protected $prot = 2; }
class Sub extends Plain { function drop() { unset($this->prot); var_dump(isset($this->prot)); } }

class Guarded {
    private $prop = "kept";
    public function __unset($k) { echo "__unset($k)\n"; dropIt($this); }
    public function show() { var_dump($this->prop); }
}
function dropIt($obj) {
    try { unset($obj->prop); } catch (Error $e) { echo $e->getMessage(), "\n"; }
}

$p = new Plain;
try { unset($p->priv); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { unset($p->prot); } catch (Error $e) { echo $e->getMessage(), "\n"; }
unset($p->absent);
(new Sub)->drop();

$g = new Guarded;
unset($g->prop);
$g->show();
