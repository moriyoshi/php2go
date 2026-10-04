<?php
$a = [];
$a[1] = "int";
$a[true] = "true overwrites 1";
$a[false] = "false is 0";
$a[null] = "null is empty string";
$a[5.0] = "whole float, silent";
$a[5.9] = "fractional float, deprecated";
$a[NAN] = "nan is 0, deprecated";
$a["9223372036854775807"] = "int64 max is an int key";
$a["9223372036854775808"] = "one over stays a string";
$a["05"] = "leading zero stays a string";
$a["1e3"] = "exponent stays a string";
$a[" 5"] = "leading space stays a string";
var_dump($a);
