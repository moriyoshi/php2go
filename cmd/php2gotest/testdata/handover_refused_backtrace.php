<?php
// debug_backtrace() hands a callee the caller's arguments.
class D {
    function __construct(public string $n) {}
    function __destruct() { echo "-{$this->n} "; }
}
function peek() { $GLOBALS['bt'] = debug_backtrace(); }
function owner($a) { peek(); echo "owner "; }
owner(new D('a'));
echo "| after owner\n";
echo count($bt), "\n";
