<?php
// A compound write or step through $GLOBALS on a global that does not exist
// warns `Undefined global variable`, as a plain read does.
function f() { $GLOBALS["g"] .= "x"; var_dump($GLOBALS["g"]); $GLOBALS["h"]++; var_dump($GLOBALS["h"]); echo $GLOBALS["nope"] ?? "dflt", "\n"; echo $GLOBALS["nope2"]; }
f();
$GLOBALS["top"] .= "y";
var_dump($top, $g);
