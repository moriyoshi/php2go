<?php
// `foreach (new A as $v)`: the loop is the object's only holder, so it is
// destroyed where the loop ends -- falling out, `break`, or a `return` from
// inside (after a `finally` there) -- before whatever follows. A destructor
// that throws throws from that point. An object its constructor stored
// elsewhere is not the loop's alone and lives on.

class A {
    public $x = 1;
    public $y = 2;
    function __construct(public string $tag = "a") {}
    function __destruct() { echo "destruct {$this->tag}\n"; }
}
class Thrower {
    public $x = 1;
    function __destruct() { echo "destruct thrower\n"; throw new Exception("from destructor"); }
}
class Kept {
    public static $all = [];
    public $x = 1;
    function __construct() { self::$all[] = $this; }
    function __destruct() { echo "destruct kept\n"; }
}

function completes() { foreach (new A("completes") as $v) {} echo "after loop\n"; }
function breaks() { foreach (new A("breaks") as $v) { break; } echo "after break\n"; }
function returns() { foreach (new A("returns") as $v) { return $v; } }
function finallyReturns() {
    foreach (new A("finally") as $v) {
        try { return $v; } finally { echo "finally\n"; }
    }
}
completes();
breaks();
var_dump(returns());
var_dump(finallyReturns());

function throws() { foreach (new Thrower as $v) { break; } echo "not reached\n"; }
try { throws(); } catch (Exception $e) { echo "caught: ", $e->getMessage(), "\n"; }

function nested() {
    foreach ([0] as $_) {
        try {
            foreach (new Thrower as $v) {
                try { break 2; } finally { echo "inner finally\n"; }
            }
        } catch (Exception $e) {
            echo "outer catch\n";
        } finally {
            echo "outer finally\n";
        }
    }
}
nested();

function kept() { foreach (new Kept as $v) {} echo "after kept loop\n"; }
kept();
echo "end\n";
