<?php
class M {
    private $hidden = "priv";
    public $shown = "pub";
    public function __get($n) { echo "get:"; echo $n; echo " "; return "G"; }
    public function __set($n, $v) { echo "set:"; echo $n; echo " "; }
    public function __call($n, $args) { return "C"; }
    public static function __callStatic($n, $args) { return "S"; }
    public function inner() { return $this->hidden; }
}
$m = new M();
echo $m->shown, "\n";
echo $m->hidden, "\n";
echo $m->nothere, "\n";
echo $m->inner(), "\n";
$m->shown = "x"; echo $m->shown, "\n";
$m->hidden = "y"; echo "\n";
$m->newprop = "z"; echo "\n";
echo $m->doIt(1,2), "\n";
echo M::statThing(), "\n";
