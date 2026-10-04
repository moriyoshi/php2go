<?php
// A static method below the owner keeps a caught exception, whose trace
// holds the owner's argument: nothing may free it when the owner returns.
class D {
    function __construct(public string $n) {}
    function __destruct() { echo "-{$this->n} "; }
}
class S {
    public static $kept;
    static function below() {
        try { throw new Exception('deep'); } catch (Exception $e) { self::$kept = $e; }
    }
}
class Owner { function take($a) { S::below(); echo "owner "; } }
function run() { $o = new Owner; $o->take(new D('a')); echo "| after\n"; }
run();
echo count(S::$kept->getTrace()), "\n";
