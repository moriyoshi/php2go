<?php
// `f($undef[])` where `f` takes a reference **creates one element**, not two.
//
// Reaching an append target on a variable that is not yet an array runs an
// autovivifying placeholder write -- which creates the array *and* appends its
// first element. Appending again to hand back a slot made a second one. The
// element to bind is the one that write already created.
//
// Only the undefined case was wrong: on an existing array there is no
// autovivification, so the single append was right, which is why an initialised
// probe passes and hides it.
function bindOnly(&$v) {}
function fill(&$v) { $v = 9; }

bindOnly($undef1[]);
var_dump($undef1);

fill($undef2[]);
var_dump($undef2);

$n = null;
bindOnly($n[]);
var_dump($n);

// An existing array keeps its contents and gains exactly one element.
$a = [];
fill($a[]);
var_dump($a);
$b = [1, 2];
fill($b[]);
var_dump($b);
$c = ['k' => 'v'];
fill($c[]);
var_dump($c);

// Two appends in a row are two elements.
$d = [];
fill($d[]);
fill($d[]);
var_dump($d);

// A plain (non-reference) append is unaffected.
$e = [];
$e[] = 1;
$e[] = 2;
var_dump($e);
echo "done\n";
