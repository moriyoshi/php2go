<?php
class B { public $a = 1; protected $b = 2; private $c = 3; public function m() {} }
class D extends B { public $d = 5; }
$d = new D();
var_dump(get_class($d));
var_dump(get_parent_class($d));
var_dump(get_parent_class("D"));
var_dump(get_parent_class("B"));
var_dump(class_exists("D"), class_exists("Nope"));
var_dump(method_exists($d, "m"), method_exists($d, "zz"));
var_dump(property_exists("D", "a"), property_exists("D", "zz"));
var_dump(class_alias("D", "Alias"), class_exists("Alias"));
