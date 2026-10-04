<?php
// A compound assignment or step on a property the object does not have
// creates it first -- with the dynamic-property deprecation -- and only then
// warns that the read found nothing; the next one is silent. stdClass warns
// without deprecating.

class Test {}
$o = new Test;
$o->a += 1;
$o->a += 1;
$o->b++;
$o->b++;
$o->c .= "x";
var_dump(get_object_vars($o));
$s = new stdClass;
$s->n -= 2;
var_dump($s->n);
