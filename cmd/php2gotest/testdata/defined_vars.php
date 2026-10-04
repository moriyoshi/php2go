<?php
// `get_defined_vars()` is answered from the body it is written in, because that
// is the only place the variable names exist. It is the variables **assigned so
// far**, in the order the slots were laid out -- one declared in a branch that
// did not run is a slot with no value and is not in the array.
function scoped($a, $b = 2) {
    $c = 3;
    var_dump(get_defined_vars());
    $d = 4;
    var_dump(array_keys(get_defined_vars()));
    if (false) {
        $never = 1;
    }
    var_dump(array_keys(get_defined_vars()));
    return $d;
}
scoped(1);

// Nothing at all.
function bare() { var_dump(get_defined_vars()); }
bare();

// A by-reference parameter is a variable like any other, and the array holds a
// copy of what it points at.
function byRef(&$r, $plain) {
    $seen = get_defined_vars();
    $r = "changed";
    var_dump($seen, get_defined_vars());
}
$outer = "before";
byRef($outer, "x");
var_dump($outer);

// The `&` the argument made is the *call's*, and it goes away with the call:
// dumping the container the argument came from afterwards shows no reference.
// (A callee that hands the binding back out -- byRef() above returns nothing,
// but get_defined_vars() puts the box in an array -- keeps it alive for as long
// as that array lives, which php2go does not track past the frame.)
function bump(&$n) { $n++; }
$box = ["slot" => 1];
bump($box["slot"]);
var_dump($box);

// Inside a method, and inside a closure.
class Holder {
    public function show($arg) { $local = 1; return array_keys(get_defined_vars()); }
}
var_dump((new Holder)->show("v"));
$fn = function ($p) use (&$outer) { $q = 2; return array_keys(get_defined_vars()); };
var_dump($fn("given"));
