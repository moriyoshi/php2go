<?php
// At the end of a request with no global holding an object, the engine walks
// the objects store in handle order, and hands out no freed handle while it
// does, so an object a destructor makes is reached later in the same walk
// (bug74053).
class D {
    function __construct(public $n) {}
    function __destruct() { echo "d{$this->n}#", spl_object_id($this), "\n"; }
}
class Maker {
    static $made = [];
    function __construct(public $n, public $left) {}
    function __destruct() {
        echo "maker{$this->n}#", spl_object_id($this), "\n";
        if ($this->left > 0) {
            self::$made[] = new Maker($this->n . "+", $this->left - 1);
        }
        $fresh = new stdClass;
        echo " fresh#", spl_object_id($fresh), "\n";
    }
}
class Holder { static $a, $b, $c, $list = []; }

// Handle order, not creation order: q is made before r, but r reuses t's
// handle, which is lower.
Holder::$a = new D('p');
$t = new stdClass;
Holder::$b = new D('q');
unset($t);
Holder::$c = new D('r');

// Held in a static array.
Holder::$list[] = new D('l1');
Holder::$list[] = new D('l2');

// A destructor that makes objects during teardown.
Holder::$list[] = new Maker('m', 2);

register_shutdown_function(function () {
    echo "shutdown\n";
    Holder::$list[] = new D('late');
    $n = new stdClass;
    echo " shutdown fresh#", spl_object_id($n), "\n";
});
echo "end\n";
