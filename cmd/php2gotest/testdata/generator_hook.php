<?php
// A get hook that yields is a generator; inside it, the property is its
// backing store at every step, not the hook again.
class A {
    public $backed = 2 {
        get {
            echo "hook started\n";
            yield 1;
            yield $this->backed;
            $this->backed = 7;
            yield $this->backed;
        }
    }
    public $virtual {
        get {
            foreach ([1, 2] as $v) {
                yield "k$v" => $v * 10;
            }
        }
    }
}
$a = new A();
$g = $a->backed;
echo "before\n";
foreach ($g as $k => $v) {
    var_dump($k, $v);
}
var_dump(iterator_to_array($a->virtual));
var_dump($a);
