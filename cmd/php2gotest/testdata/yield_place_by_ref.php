<?php
// A by-reference generator yielding an element or a property yields its
// box: `foreach (... as &$v)` writes through it, and only the box the loop
// variable still holds is shown as a reference afterwards.
function &elements(array &$array) {
    yield $array[0];
    yield "k" => $array[2];
}
$array = [1, 2, 3];
foreach (elements($array) as $k => &$val) {
    var_dump($k);
    $val *= -1;
}
unset($val);
var_dump($array);

$o = new class {
    public int $a = 1;
    public int $b = 10;
    public function &props() {
        yield $this->a;
        yield $this->b;
    }
};
foreach ($o->props() as &$p) {
    $p += 1;
}
var_dump($o);
