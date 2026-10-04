<?php
// A library function that calls another library function only from inside a
// closure still links it: array_multisort compares through a helper its usort
// callback calls.

$a = ["row1" => 2, "row2" => 1, "row3" => 3];
var_dump(array_multisort($a), $a);
$data = [3, 1, 2];
$other = ["c", "a", "b"];
array_multisort($data, SORT_DESC, $other);
var_dump($data, $other);
