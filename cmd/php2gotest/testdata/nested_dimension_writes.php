<?php
$a = [];
$a['x']['y'] = 1;
$a['x']['z'][] = 2;
$a['n'][3] = "q";
var_dump($a);
$b = ['s' => 'abc'];
try { $b['s']['t'] = 1; } catch (\Error $e) { echo $e->getMessage(), "\n"; }
$c = ['i' => 5];
try { $c['i']['j'] = 1; } catch (\Error $e) { echo $e->getMessage(), "\n"; }
class C {
    public $d = [];
    public $u;
    public function set($k, $v) { $this->d[$k] = $v; }
    public function push($v) { $this->d[] = $v; }
    public function nest($k, $j, $v) { $this->d[$k][$j] = $v; }
    public function auto($k, $v) { $this->u[$k] = $v; }
}
$o = new C;
$o->set("a", 1);
$o->push(2);
$o->nest("m", "n", 3);
$o->auto("z", 4);
var_dump($o->d, $o->u);
echo "done\n";
