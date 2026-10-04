<?php
// A by-reference foreach variable is each element's box in turn, so a
// store into it writes the element -- whatever the stored value is.
class Box { public $v; function __construct($v) { $this->v = $v; } }
function a() { $arr = [1, 2]; foreach ($arr as &$v) { $v = new Box(count($arr)); } unset($v); var_dump($arr); }
function b() { $arr = ['p', 'q']; foreach ($arr as &$v) { $v .= "x"; $v .= "y"; } unset($v); var_dump($arr); }
function c() { $arr = [1, 2]; foreach ($arr as $k => &$v) { $v = $k + 100; } unset($v); var_dump($arr); }
function d() { $arr = [[1], [2]]; $o = [9]; foreach ($arr as &$v) { $v = $o; } $o[] = 5; unset($v); var_dump($arr); }
a(); b(); c(); d();
$top = [1, 2]; foreach ($top as &$t) { $t = new ArrayObject([]); } unset($t); var_dump(count($top), get_class($top[0]));

class Holder {
    public $foo = 1;
    public $bar = 1;
    function __destruct() { echo "destruct: ", is_object($this->foo) ? get_class($this->foo) : var_export($this->foo, true), "\n"; }
}
function viaTry() {
    foreach (new Holder as &$foo) {
        try {
            $foo = new Exception("in loop");
            return;
        } catch (Exception $e) {
            echo "unreachable\n";
        }
    }
}
viaTry();
echo "after\n";
