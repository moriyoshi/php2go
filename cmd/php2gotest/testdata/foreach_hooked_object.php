<?php
// Iterating an object whose class has property hooks: the declared
// properties in declaration order, then the dynamic ones -- each read when
// its element is reached, so a get hook runs inside the loop. A write-only
// virtual property is skipped, a backed one with only a set hook reads its
// slot (and is skipped while uninitialized), a hook inherited from the
// parent runs in the parent's scope, and a property the body adds is reached.
// By reference, a `&get` hook hands out its box and a plain `get` is refused.

#[AllowDynamicProperties]
class Base {
    public $plain = 'plain';
    private $_store = 'stored';
    public $virt {
        get { echo "  [get virt]\n"; return $this->_store; }
        set { $this->_store = $value; }
    }
    public $writeOnly { set { echo "never\n"; } }
    public int $setOnly { set => $value * 2; }
}
class Child extends Base {
    public $own = 'own';
}

$c = new Child;
foreach ($c as $k => $v) {
    echo "$k => $v\n";
    if ($k === 'plain') {
        $c->added = 'added in the loop';
    }
}

class Refs {
    private $_x = 1;
    public $byRef { &get { return $this->_x; } }
    public $byVal { get => 'value'; }
}
$r = new Refs;
try {
    foreach ($r as $k => &$v) {
        echo "$k: ", var_export($v, true), "\n";
        if ($k === 'byRef') {
            $v = 99;
        }
    }
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
unset($v);
var_dump($r->byRef);
