<?php
// get_defined_vars() at file scope is the global table, superglobals first,
// then the file's variables in order; unset ones are not there.
$a = 1;
$b = [2];
$c = null;
unset($c);
$vars = get_defined_vars();
var_dump(array_keys($vars));
var_dump($vars["a"], $vars["b"]);
foreach (get_defined_vars() as $key => $value) {
    if ($key === "b") {
        unset($$key);
    }
}
var_dump(isset($b), array_key_exists("b", get_defined_vars()));

// In the engine's order -- a foreach's value before its key -- and with no
// compiler temporaries among them.
foreach ([1] as $k => $v) {
}
$last = 1;
var_dump(implode(",", array_keys(get_defined_vars())));
var_dump(implode(",", array_keys($GLOBALS)));
