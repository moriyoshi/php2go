<?php
@$r = 5;
var_dump($r);
@$s = new stdClass;
@$s->x = 3.5;
var_dump($s->x);
$t = ['k' => 1];
@$t['k'] = 2;
var_dump($t);
@$u++;
var_dump($u);
// A suppressed write of an object: the target is a place, not a frame slot.
class Q { public $n = 1; }
@$w = new Q;
var_dump($w->n);
