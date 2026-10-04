<?php
// A library function written in PHP is one engine function to a trace:
// no frames of its own inside, callbacks from it are [internal function],
// and a failure inside it names the caller's line.
class X {}
function w(&$v, $k) { if ($k === 1) { debug_print_backtrace(); } }
$aw = [[0, 1]];
array_walk_recursive($aw, "w");
$r = array_find([1, 2, 3], function ($v) { if ($v == 2) { echo (new Exception)->getTraceAsString(), "\n"; } return $v == 3; });
var_dump($r);
try { str_pad("x", 3, ""); } catch (ValueError $e) { echo $e->getMessage(), " @", $e->getLine(), "\n", $e->getTraceAsString(), "\n"; }
function t() {
    $a = [1 => new X, 0 => new X];
    array_multisort($a, SORT_STRING);
}
try { t(); } catch (Error $e) { echo $e->getMessage(), " @", $e->getLine(), "\n", $e->getTraceAsString(), "\n"; }

// A parent's private hooked property shadowed by the child's: each runs
// its own get hook, from its own scope, and var_export lists both.
class P {
    private $changed = 'changed P' {
        get { return strtoupper($this->changed); }
    }
    public $plain = 'plain';
    function fromP() { var_dump(get_object_vars($this)); }
}
class C extends P {
    private $changed = 'changed C' {
        get { return strtoupper($this->changed) . '!'; }
    }
    function fromC() { var_dump(get_object_vars($this)); }
}
$o = new C;
$o->fromP();
$o->fromC();
var_export($o);
echo "\n";
var_dump(get_object_vars($o));
$b = [2 => new stdClass];
array_multisort($b, SORT_STRING);
