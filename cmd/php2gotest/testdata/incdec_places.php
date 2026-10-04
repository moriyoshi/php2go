<?php
$a = ["k" => 1];
$a["k"]++;
++$a["k"];
var_dump($a);
$b = [];
$b["n"]++;
var_dump($b);
class C { public $p = 1; }
$o = new C;
$o->p++;
var_dump($o->p);
$c = ["s" => "az"];
$c["s"]++;
var_dump($c);
$d = ["k" => 5];
var_dump($d["k"]++, $d["k"], ++$d["k"]);
$e = ["m" => 3];
$e["m"]--;
--$e["m"];
var_dump($e);
