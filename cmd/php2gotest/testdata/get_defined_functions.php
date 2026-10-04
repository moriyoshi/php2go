<?php
// get_defined_functions(): the engine's functions in its own order, and the
// program's, lowercased, in the order they were declared -- a conditional
// one once it has been.
function First() {}
function second() {}
$f = get_defined_functions();
var_dump(array_keys($f), array_slice($f["internal"], 0, 6), count($f["internal"]));
var_dump(in_array("strlen", $f["internal"]), in_array("str_pad", $f["internal"]));
var_dump($f["user"]);
if (true) {
    function Later() {}
}
var_dump(get_defined_functions()["user"]);
var_dump(get_defined_functions(true) === get_defined_functions());
get_defined_functions(false);
