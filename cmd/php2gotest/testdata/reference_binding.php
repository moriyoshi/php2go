<?php
// Binding through an array element, a property, and a nested element.
$a = ['k' => 1];
$r = &$a['k'];
$r = 2;
var_dump($a, $r);
class C { public $p = 5; public $d = ['q' => 6]; }
$o = new C;
$s = &$o->p;
$s = 7;
$t = &$o->d['q'];
$t = 8;
var_dump($o->p, $o->d);
// Binding to an element that does not exist yet creates it.
$b = [];
$u = &$b['new'];
var_dump($b);
$u = 9;
var_dump($b);
// A nested write next to a binding: the write's box must not survive, the
// binding's must.
$c = [];
$c['x']['y'] = 1;
var_dump($c);
$v = &$c['x']['y'];
var_dump($c);
echo "done\n";
