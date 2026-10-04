<?php
// A read-write fetch through a fresh receiver whose property's set hook
// stores `$this`: the receiver outlives the statement. A file of its own,
// because a leaking `__set` elsewhere would refuse it for another reason
// (temporary_operator_consumers_kept).

class Q { static $quiet = false; }
class KeepH {
    static $kept;
    public string $foo = "" {
        set { self::$kept = $this; $this->foo = $value; }
    }
    function __destruct() { if (!Q::$quiet) echo "~KeepH\n"; }
}
var_dump((function () { return new KeepH; })()->foo .= "x");
echo "after keepH (kept by a set hook)\n";
echo "end\n";
// Teardown order is not what this tests.
Q::$quiet = true;
