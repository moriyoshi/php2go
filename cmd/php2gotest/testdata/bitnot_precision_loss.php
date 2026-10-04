<?php
// `~1.5` converts to int and **deprecates the precision loss**, exactly as the
// binary integral operators do.
//
// They reach that through `AcceptsArithmetic`, which takes two operands and so
// cannot serve a unary one -- which is why `~` was the only bitwise operator
// still silent, and why the catalogue entry for `BitNot` was the only one
// lacking the `Deprecates` effect its siblings all carry. An effect annotation
// that does not match the implementation is a claim, not a description.
$f = 1.5;
var_dump(~$f);
var_dump($f | 3, $f & 3, $f ^ 3, $f << 3, $f >> 3);

// An **integral** float loses nothing, so it says nothing.
$g = 2.0;
var_dump(~$g);
var_dump($g | 1);

// A float-shaped string has its own wording ("from float-string").
$s = "1.5";
var_dump(~$s);

// `~` on a string is bytewise: no conversion, so no deprecation.
var_dump(~"AB");

// An int is untouched either way.
var_dump(~5, ~-1);
echo "done\n";
