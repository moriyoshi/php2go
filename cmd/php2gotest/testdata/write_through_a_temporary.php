<?php
// Writing a property on what a call returned: the target is a temporary, and
// the write has to land on the object the call produced.
class Box { public $v = 0; }
class Maker {
    public $b;
    public function __construct() { $this->b = new Box; }
    public function get() { return $this->b; }
    public function nothing() {}
}
$m = new Maker;
$m->get()->v = 7;
var_dump($m->b->v);
// The same on a call that returns null.
try { $m->nothing()->v = 1; } catch (\Error $e) { echo $e->getMessage(), "\n"; }
var_dump($m->nothing());
echo "done\n";
