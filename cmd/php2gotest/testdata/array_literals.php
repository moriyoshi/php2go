<?php
$list = [1, 2, 3];
$map = ["x" => 1, "y" => "two"];
$empty = [];
$nested = [[1, 2], [3]];
$mixed = [5 => "a", "k" => [true, null, 1.5]];
var_dump($list, $map, $empty, $nested, $mixed);
