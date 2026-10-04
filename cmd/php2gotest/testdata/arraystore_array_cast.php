<?php
// (array) of an ArrayObject or ArrayIterator is its storage, not its
// properties -- unless STD_PROP_LIST asked for the properties.
#[AllowDynamicProperties]
class Bag extends ArrayObject {
    public $p = 1;
}
$a = new Bag(["x" => 1, "y" => [2]]);
$a->d = 2;
var_export((array) $a);
echo "\n";
var_export(get_object_vars($a));
echo "\n";
var_export((array) new ArrayObject(["y" => 2], ArrayObject::STD_PROP_LIST));
echo "\n";
var_export((array) new ArrayIterator(["z" => 3]));
echo "\n";
$o = new stdClass;
$o->q = 1;
var_export((array) new ArrayObject($o));
echo "\n";
var_export((array) new ArrayObject(new ArrayObject(["n" => 1])));
echo "\n";
// The cast is a copy: writing to it leaves the storage alone.
$copy = (array) $a;
$copy["x"] = 99;
var_dump($a["x"]);
