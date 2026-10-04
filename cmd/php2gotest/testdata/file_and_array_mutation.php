<?php
// The array functions that rewrite their argument: they take it by reference,
// and the caller sees the change through the box the send passed in.
$a = [1, 2, 3];
var_dump(array_shift($a), $a);
var_dump(array_push($a, 9, 10), $a);
var_dump(array_unshift($a, 0), $a);
$empty = [];
var_dump(array_shift($empty), $empty);
// String keys survive a shift; integer keys renumber.
$named = ["x" => 1, 5 => "five", "y" => 2];
var_dump(array_shift($named), $named);

// Reading and writing a file, which is the host answering directly.
$path = "/tmp/php2go-file-and-array-test";
var_dump(file_put_contents($path, "written\nhere"));
var_dump(file_get_contents($path));
var_dump(is_resource($path), is_resource(fopen($path, "r")));
var_dump(unlink($path));
var_dump(file_get_contents($path));
var_dump(unlink($path));
