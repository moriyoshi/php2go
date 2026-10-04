<?php
// Assigning a property reads the value loudly -- `$o->$p = $v` warns about an
// undefined $v, and before the undefined name $p -- while a dimension write
// and a compound property write read the name first. The receiver of a write
// is fetched silently.

$o = new stdClass;
$o->$p = $v;
$a = [];
$a[$k] = $w;
$o->{$q} .= $x;
$o->plain = $undefined;
var_dump($o);
