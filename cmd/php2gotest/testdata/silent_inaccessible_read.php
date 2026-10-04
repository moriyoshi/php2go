<?php
class A { private $p = 7; protected $q = 8; }
$a = new A;
var_dump(isset($a->p));
var_dump($a->p ?? "none");
var_dump(empty($a->q));
try { var_dump($a->p); } catch (Error $e) { echo $e->getMessage(), "\n"; }
echo "done\n";
