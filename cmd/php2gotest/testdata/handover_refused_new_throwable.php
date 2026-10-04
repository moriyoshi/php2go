<?php
// A throwable made and kept takes a trace -- with the owner's argument.
class D {
    function __construct(public string $n) {}
    function __destruct() { echo "-{$this->n} "; }
}
function make() { $GLOBALS['ex'] = new RuntimeException('kept'); }
function owner($a) { make(); echo "owner "; }
owner(new D('a'));
echo "| after owner\n";
echo $ex->getMessage(), "\n";
