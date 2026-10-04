<?php
// Elements and properties sent to a function declared later reach it by
// reference where **their** parameter is by reference -- decided by each
// argument's position, named or not, not by how many earlier arguments were
// references.

$v = [0, 0, 0];
mixed($v[0], $v[1], $v[2]);
echo implode(" ", $v), "\n";

$v = [0, 0, 0];
mixed(c: $v[2], a: $v[0]);
echo implode(" ", $v), "\n";

$o = new stdClass;
$o->p = 0;
$o->q = 0;
mixed($o->p, $o->q);
echo "$o->p $o->q\n";

function mixed($a = null, &$b = null, &$c = null)
{
    $a = "a";
    $b = "b";
    $c = "c";
}
