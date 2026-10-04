<?php
// A read-modify-write through an offset that needs converting -- a resource,
// a fractional float -- reports the conversion once, at the fetch.
$r = fopen('/dev/null', 'r');
$a = [];
$a[$r] = 1;
$a[$r] += 1;
$a[$r]++;
$a[$r] .= "x";
var_dump($a);
$b = [];
$b[1.5] = 1;
$b[1.5] += 1;
$b[2.5]++;
var_dump($b);
