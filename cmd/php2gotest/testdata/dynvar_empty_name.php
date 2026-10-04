<?php
// `$$n` reads its name once, and with `$n` unset names the variable "": an
// argument read that way warns `Undefined variable $` and passes NULL.

show(0, $$missing);
$name = "";
$$name = "empty-named";
show(0, $$name);
$n = "x";
show(0, $$n);

// Declared after the calls, so each argument is fetched for a callee the
// compiler has not seen yet.
function show($a, $b) { var_dump($b); }
