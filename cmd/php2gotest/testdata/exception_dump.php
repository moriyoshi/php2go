<?php
class MyEx extends RuntimeException {}
$a = new Exception("plain");
$b = new MyEx("derived");
var_dump($a);
var_dump($b);
