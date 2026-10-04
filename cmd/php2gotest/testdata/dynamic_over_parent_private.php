<?php
// Writing a name that is an ancestor's private property, from a scope that
// cannot see it, creates a dynamic property beside the private one rather
// than overwriting it: the ancestor's methods go on reading their private,
// everything else reads the new one, and the dump lists both in table order
// (foreach_shadowed_dyn_property). Another instance of the class is not
// affected.

class Test {
    private $prop = "Test";
    public $after = "after";

    function run() {
        foreach ($this as $k => $v) {
            echo "$k => $v\n";
        }
        var_dump(get_object_vars($this));
        var_dump($this->prop);
    }
    function runRef() {
        foreach ($this as $k => &$v) {
            echo "$k => $v\n";
        }
    }
    function set($v) {
        $this->prop = $v;
    }
}

#[AllowDynamicProperties]
class Test2 extends Test {
}

class Test3 extends Test {
}

$test2 = new Test2;
$other = new Test2;
$test2->prop = "Test2";
var_dump($test2);
$test2->run();
$test2->runRef();
$test2->set("set inside");
$test2->prop .= "!";
var_dump($test2->prop, isset($test2->prop));
$test2->run();
var_dump(get_object_vars($test2));
$other->run();
var_dump($other);

$t3 = new Test3;
$t3->prop = "deprecated";
var_dump($t3);
unset($t3->prop);
var_dump(isset($t3->prop));
$t3->run();
