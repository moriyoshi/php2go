<?php
// Globals that hold objects are destroyed backwards at the end of a request,
// before anything else.
class D {
    function __construct(public $n) {}
    function __destruct() { echo "d{$this->n}\n"; }
}
$a = new D('a');
$b = new D('b');
$c = new D('c');
echo "end\n";
