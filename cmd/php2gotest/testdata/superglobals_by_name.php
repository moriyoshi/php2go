<?php
// `$_SERVER` and `$_ENV` are created only for a script that *spells* one:
// `$GLOBALS["_SERVER"]` compiles to the same fetch and does not create it, nor
// does a variable variable. The four request arrays exist regardless.

var_dump(array_keys($GLOBALS));
var_dump(isset($GLOBALS["_SERVER"]), $GLOBALS["_GET"]);
$n = "_ENV";
var_dump(isset($$n));
function f()
{
    return $GLOBALS["_SERVER"] ?? "absent";
}
var_dump(f());
var_dump(array_keys($GLOBALS));
