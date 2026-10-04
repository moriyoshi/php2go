<?php
class R {
    public function __get($n) { echo "enter "; return $this->missing; }
}
$r = new R();
var_dump($r->missing);
echo "survived\n";
