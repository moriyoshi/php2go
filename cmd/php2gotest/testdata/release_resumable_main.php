<?php
// Releases in a file body the split made resumable: the top-level `inner()`
// may suspend, so `{main}` is a state machine. Its locals are the globals and
// stay unpromoted, and `unset`/overwrite release what they own outright, on
// either side of the split. A local some function binds as a global is not
// the body's alone and is left alone.
class D {
    function __construct(public $n) {}
    function __destruct() { echo "D{$this->n} gone\n"; }
}
function inner() {
    try {
        return Fiber::suspend('s');
    } catch (FiberError $e) {
        return 'outside';
    }
}
class Keep { public static $o; }
function peek() { global $shared; Keep::$o = $shared; return $shared->n; }
$a = new D(1); unset($a); echo "after unset\n";
$b = new D(2); $b = null; echo "after overwrite\n";
echo inner(), "\n";
$c = new D(3); unset($c); echo "after unset past the split\n";
$k = new D(4); $k = new D(5); echo "after reassign\n";
unset($k);
$shared = new D(6);
echo peek(), "\n";
unset($shared); echo "after unset of a global another body binds\n";
echo "end\n";
