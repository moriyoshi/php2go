<?php
class B { private $priv = 1; protected $prot = 2; public $pub = 3;
    public function own() { return $this->priv; } }
class D extends B { public function fromSub() { return $this->prot; }
    public function subPriv() { return $this->priv; } }
class U { public function peek($o) { return $o->prot; } }
$b = new B(); $d = new D(); $u = new U();
echo $b->own(), "\n";
echo $d->fromSub(), "\n";
echo $b->pub, "\n";
echo $d->subPriv(), "\n";
try { echo $b->priv; } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { echo $d->priv; } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { echo $u->peek($d); } catch (Error $e) { echo $e->getMessage(), "\n"; }
echo "end\n";
