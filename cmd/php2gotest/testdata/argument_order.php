<?php
// A by-value argument that names a variable is taken where it is sent, before
// a later argument can change the variable.
function show(...$args) { var_dump($args); }
function setRef(&$r) { $r = "set by ref"; return "ret"; }
function t()
{
    $a = 0; show($a, $a = 1);
    $i = 0; show($i, $i++, $i);
    $l = [1]; show($l, $l[] = 2);
    $m = [1]; show($m, $m[0] = 3);
    $s = "s"; show($s, $s .= "x");
    $x = "x"; show($x, setRef($x), $x);
    $o = new stdClass; $o->p = 1; show($o, $o = new ArrayObject([]));
    $u = 5; show($u, $u);
}
t();
// At file scope too, where the variables are globals.
$g = "before"; show($g, $g = "after");
