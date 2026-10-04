<?php
// Inside either hook of a property, that property on the same object is its
// backing field for reads and writes alike: the get hook writing it does not
// run the set hook, the set hook reading it does not run the get hook. The
// guard is per object -- the same property on another object still runs its
// hooks.

class Test {
    public ?Test $other = null;
    public $prop {
        get {
            echo "get\n";
            $this->prop = 'from get';
            if ($this->other) {
                $this->other->prop = 'via other';
            }
            return $this->prop;
        }
        set {
            echo "set\n";
            var_dump($this->prop);
            $this->prop = $value;
        }
    }
}

$test = new Test;
var_dump($test->prop);
var_dump((array)$test);
$test->prop = 42;
var_dump((array)$test);

$a = new Test;
$a->other = new Test;
var_dump($a->prop);
