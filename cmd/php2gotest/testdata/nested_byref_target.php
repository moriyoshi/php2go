<?php
// A **nested** write target opens its parent as a box and builds the inner
// place inside it. That box is a mechanism the program never asked for and must
// be collapsed again -- but not until the place has been *used*.
//
// Releasing it where the place was built destroyed the storage before anything
// wrote through it, so `byref($a["k"]["j"])` left `["k"] => NULL`. The release
// now travels with the place and whoever uses it runs it.
function byref(&$x) { $x = 5; }

$a = [];
byref($a["k"]["j"]);
var_dump($a);

$b = ['k' => []];
byref($b["k"]["j"]);
var_dump($b);

// Three deep, and a mixed existing/missing path.
$c = [];
byref($c["x"]["y"]["z"]);
var_dump($c);
$d = ['x' => ['y' => []]];
byref($d["x"]["y"]["z"]);
var_dump($d);

// An explicit reference to a nested element keeps its `&` marker.
$e = [];
$r = &$e["k"]["j"];
$r = 7;
var_dump($e);

// A plain nested write is unaffected, and neither leaves a stray box behind.
$f = [];
$f["k"]["j"] = 9;
var_dump($f);

// Object properties nest the same way.
class Box { public $inner; }
$o = new Box();
byref($o->inner["k"]);
var_dump($o);

// A dynamic callee reaching a nested target.
$g = "byref";
$h = [];
$g($h["k"]["j"]);
var_dump($h);
echo "done\n";
