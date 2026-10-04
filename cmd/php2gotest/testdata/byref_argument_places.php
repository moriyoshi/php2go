<?php
function f(&$x) { $x = $x + 1; }
$a = ['k' => 1];
f($a['k']);
var_dump($a);
class C { public $p = 5; public $d = ['q' => 6]; }
$o = new C;
f($o->p);
f($o->d['q']);
var_dump($o->p, $o->d);
$b = [];
f($b['new']);
var_dump($b);
$l = 1;
f($l);
var_dump($l);
// Mixed by-value and by-reference arguments: the prelude blocks are numbered by
// how many were opened, not by argument position.
function g($a, &$b, $c, &$d) { $b = $b . "B"; $d = $d . "D"; }
$x = ['p' => 'p', 'q' => 'q'];
g(1, $x['p'], 2, $x['q']);
var_dump($x);
