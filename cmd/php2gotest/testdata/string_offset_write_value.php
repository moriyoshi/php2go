<?php
// Writing a string offset answers the one byte written, whatever was
// assigned; an offset before the start warns, writes nothing and answers
// NULL.

$s = "abc";
var_dump($s[1] = "xyz");
var_dump($s[5] = 7);
var_dump($s);
var_dump($s[-9] = "q");
var_dump($s);
$i = -1;
$r = $s[$i] = "Z";
var_dump($r, $s);
