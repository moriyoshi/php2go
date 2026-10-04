<?php
class B { private $priv = 1; protected $prot = 2; public $pub = 3;
    public function setOwn($v) { $this->priv = $v; return $this->priv; } }
$b = new B();
echo $b->setOwn(9), "\n";
$b->pub = 4; echo $b->pub, "\n";
try { $b->priv = 5; } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { $b->prot = 6; } catch (Error $e) { echo $e->getMessage(), "\n"; }
echo "end\n";
