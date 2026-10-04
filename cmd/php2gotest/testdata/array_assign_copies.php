<?php
// **Assigning an array copies it**, so storing one into itself stores what it
// was -- not a loop back into itself. `*RECURSION*` is reserved for a structure
// genuinely made recursive through a reference.
//
// Without the copy on a dimension write, `$a[0] = $a` left the element holding
// the same array and every dump reported `*RECURSION*`, which is the engine's
// answer to a different program.
$a = [];
$a[0] = $a;
var_dump($a);

$b = [1, 2];
$b[] = $b;
var_dump($b);

$c = ['x' => 1];
$c['self'] = $c;
var_dump($c);

// Nested, and through a second variable: still copies.
$d = ['k' => ['inner' => 1]];
$d['k']['copy'] = $d['k'];
var_dump($d);
$e = [1];
$f = $e;
$e[] = $f;
var_dump($e, $f);

// A genuine reference DOES recurse, and both dumpers say so.
$g = [1];
$g[] = &$g;
var_dump($g);
echo print_r($g, true), "\n";

// print_r's recursion guard rests on array identity, which an eager copy
// destroys -- so its subject is taken by reference. Literals still work.
print_r([1, 2]); echo "\n";
print_r("lit"); echo "\n";
var_dump(print_r([3], true));
echo "done\n";
