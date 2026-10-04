<?php
function add($carry, $item) { return $carry + $item; }
$sum = array_reduce([1, 2, 3, 4], 'add', 0);
var_dump($sum);
$empty = array_reduce([], 'add', 7);
var_dump($empty);
