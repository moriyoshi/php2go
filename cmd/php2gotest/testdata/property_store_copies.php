<?php
// Storing an array into a property gives the property its own array. The
// compiled property and static-property stores kept the pointer instead, so a
// write through either side reached the other. Found while settling a rule of
// the ownership trace: that storing into a container copies, which PHP says and
// the dimension and array stores already did.
class O
{
    public $a;
    public array $typed = [];
    public static $s;
}

function viaProperty($x)
{
    $arr = [$x];
    $o = new O();
    $o->a = $arr;
    $o->a[] = 2;
    $arr[] = 3;
    var_dump($arr, $o->a);
}

function viaTypedProperty($x)
{
    $arr = [$x];
    $o = new O();
    $o->typed = $arr;
    $o->typed[] = 2;
    var_dump($arr, $o->typed);
}

function viaDynamicProperty($x)
{
    $arr = [$x];
    $o = new stdClass();
    $o->d = $arr;
    $o->d[] = 2;
    var_dump($arr, $o->d);
}

function viaStatic($x)
{
    $arr = [$x];
    O::$s = $arr;
    O::$s[] = 2;
    $arr[] = 3;
    var_dump($arr, O::$s);
}

function fromParameter(array $arr)
{
    $o = new O();
    $o->a = $arr;
    $o->a[] = 2;
    return $arr;
}

// The same shapes that already copied, as the control.
function viaElement($x)
{
    $arr = [$x];
    $list = [];
    $list[] = $arr;
    $list[0][] = 4;
    var_dump($arr, $list);
}

viaProperty(1);
viaTypedProperty(1);
viaDynamicProperty(1);
viaStatic(1);
var_dump(fromParameter([1]));
viaElement(1);

$top = [1];
$o = new O();
$o->a = $top;
$o->a[] = 2;
var_dump($top, $o->a);
