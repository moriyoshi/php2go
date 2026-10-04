<?php
class Counter {
    public $n = 0;
    public $label = "c";
    public function __construct($start) {
        $this->n = $start;
    }
    public function bump($by) {
        $this->n = $this->n + $by;
        return $this->n;
    }
    public function name() {
        return $this->label;
    }
}
$a = new Counter(10);
$b = new Counter(100);
echo $a->bump(3), ",", $a->bump(4), ",", $a->n, "\n";
echo $b->bump(1), ",", $b->n, ",", $b->name(), "\n";
echo $a->missing, "|\n";
