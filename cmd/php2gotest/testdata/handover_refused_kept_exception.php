<?php
// A function below the owner keeps a caught exception, whose trace holds
// the owner's argument: nothing may free it when the owner returns.
class D {
    function __construct(public string $n) {}
    function __destruct() { echo "-{$this->n} "; }
}
function below() { try { throw new Exception('deep'); } catch (Exception $e) { $GLOBALS['kept'] = $e; } }
function owner($a) { below(); echo "owner "; }
owner(new D('a'));
echo "| after owner\n";
echo count($kept->getTrace()), "\n";
