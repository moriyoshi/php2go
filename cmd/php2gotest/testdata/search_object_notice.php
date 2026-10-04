<?php
class X {}
$o = new X;
var_dump(in_array($o, [1, 2]));
var_dump(in_array($o, [1.5]));
var_dump(array_search($o, [3, 1]));
var_dump($o == 1);
var_dump(1 == $o);
var_dump($o == 1.0);
var_dump($o != 2);
var_dump($o < 5);
var_dump($o <=> 1);
var_dump($o == "1");
var_dump($o == true);
var_dump($o == null);
var_dump(in_array($o, ["a", 1], false));
var_dump(in_array(1, [$o]));
var_dump(array_keys([1, 2, $o], 1));
switch ($o) { case 1: echo "one\n"; break; default: echo "def\n"; }
var_dump(match(true) { $o == 1 => "m", default => "d" });
$s = new stdClass;
var_dump($s == 0);
var_dump(max($o, 1));
