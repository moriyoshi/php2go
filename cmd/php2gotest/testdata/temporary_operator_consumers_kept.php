<?php
// The operation's own code keeps the temporary -- `__toString`, `offsetGet`,
// `__isset`, `__set` store `$this` -- so it must outlive the statement
// (temporary_operator_consumers holds the cases that do die). Each class here
// poisons its method name for the whole program, which is why these live in a
// file of their own.

class Q { static $quiet = false; }
echo "-- kept by the code the operation runs\n";
class Keep {
    static $kept = [];
    public $foo = 1;
    function __toString() { self::$kept[] = $this; return "K"; }
    function __destruct() { if (!Q::$quiet) echo "~Keep\n"; }
}
var_dump(new Keep . "x");
echo "after keep (kept by __toString)\n";
class KeepA implements ArrayAccess {
    static $kept;
    function offsetGet($x): mixed { self::$kept = $this; return 1; }
    function offsetSet($x, $y): void {}
    function offsetExists($x): bool { return true; }
    function offsetUnset($x): void {}
    function __destruct() { if (!Q::$quiet) echo "~KeepA\n"; }
}
var_dump((new KeepA)[0]);
echo "after keepA (kept by offsetGet)\n";
class KeepI {
    static $kept;
    function __isset($n) { self::$kept = $this; return true; }
    function __get($n) { return new stdClass; }
    function __destruct() { if (!Q::$quiet) echo "~KeepI\n"; }
}
var_dump(isset((new KeepI)->foo->bar));
echo "after keepI (kept by __isset)\n";
class KeepS {
    static $kept;
    public $bar = 1;
    function __set($n, $v) { self::$kept = $this; }
    function __get($n) { return 1; }
    function __destruct() { if (!Q::$quiet) echo "~KeepS\n"; }
}
var_dump((function () { return new KeepS; })()->foo++);
echo "after keepS (kept by __set)\n";

echo "end\n";
// Teardown order is not what this tests.
Q::$quiet = true;
