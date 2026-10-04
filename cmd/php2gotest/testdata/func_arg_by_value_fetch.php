<?php
// An argument fetched for a callee that decides -- `f($a["k"])` -- is a read
// when the parameter is by value: a missing key warns and nothing is created,
// and `$a[]` is `Cannot use [] for reading`. For a by-reference parameter it
// is a write fetch: the key is created silently, and `[]` appends
// (func_arg_fetch_optimization). The functions are declared after their
// calls, so the compiler cannot settle the mode itself.

$a = ["k" => 1];
var_dump(val($a["k"]), val($a["missing"]), val($a["missing"]["deeper"]));
var_dump($a);
ref($a["new"]);
ref($a[]);
ref($a["n1"]["n2"]);
var_dump($a);
try {
    val($a[]);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    val($a[]["x"]);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
$o = new stdClass;
var_dump(val($o->nope));
ref($o->made);
print_r($o);
function test($x) {
    test($x[][$y]);
}
try {
    test([]);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}

function val($x) { return $x; }
function ref(&$x) { $x = "set"; }
