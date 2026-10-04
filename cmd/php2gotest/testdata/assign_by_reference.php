<?php
// A property becomes a reference: writing either name moves both.
class C { public $p = 1; }
$o = new C;
$v = 5;
$o->p =& $v;
$v = 9;
var_dump($o->p, $o);
$o->p = 3;
var_dump($v);

// An array element, and one that does not exist yet.
$a = [];
$w = 1;
$a["k"] =& $w;
$w = 2;
var_dump($a);

// `=& f()` where f does not return by reference: a notice, and a copy -- the
// target holds a plain value, so var_dump shows no `&`.
function notRef() { return 42; }
$b = [];
$b["j"] =& notRef();
var_dump($b);
$q =& notRef();
$q = 1;
var_dump(notRef());

// The other arm: `=& f()` where f DOES return by reference takes the box and
// emits no notice.
function &pick(array &$src, $k) { return $src[$k]; }
$holder = ["x" => 7];
$r =& pick($holder, "x");
$r = 100;
var_dump($holder);
echo "done\n";
// MAKE_REF also appears over a write fetch, where the source IS a variable and
// there is nothing to notice: a self-referential element.
$s = [[1]];
$s[0][] =& $s[0];
$s[0][0] = 2;
var_dump($s[0][0]);
