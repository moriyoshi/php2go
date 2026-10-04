<?php
$a = 1;
$arr = ["k" => 1, "n" => null];
class C { public $p = 1; public $q = null; private $hidden = 2; }
$o = new C();
var_dump(isset($a), isset($undef));
var_dump(isset($arr["k"]), isset($arr["n"]), isset($arr["zz"]));
var_dump(isset($o->p), isset($o->q), isset($o->zz), isset($o->hidden));
var_dump(empty($a), empty($arr["n"]), empty($arr["zz"]));
// empty is not !isset: a set-but-falsy value is empty.
$zero = 0;
var_dump(isset($zero), empty($zero));
unset($a);
var_dump(isset($a));
unset($arr["k"]);
var_dump($arr);
unset($o->p);
var_dump($o);
// A string offset.
$s = "abc";
var_dump(isset($s[1]), isset($s[9]));
