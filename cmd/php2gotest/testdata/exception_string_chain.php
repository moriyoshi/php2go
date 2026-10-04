<?php
// Exception::__toString() renders the previous-chain: the root cause first,
// then each exception that wrapped it as `Next ...` -- by cast, by echo, and
// by calling the method.

$root = new LogicException("root");
$mid = new RuntimeException("middle", 1, $root);
$top = new Exception("top", 2, $mid);
var_dump((string)$top);
echo $mid, "\n";
var_dump($root->__toString());
var_dump(strlen($top->__toString()) === strlen((string)$top));
