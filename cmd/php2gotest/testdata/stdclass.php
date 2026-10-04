<?php
$o = new stdClass();
$o->a = 1;
$o->b = "x";
var_dump($o);
var_dump($o->missing);
var_dump(get_class($o));
