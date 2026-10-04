<?php
// **A string literal may contain " as ".** The IR renders a named send as
// `byval <operand> as "<name>"`, and the emitter was looking for that with a
// substring search -- so `var_dump("thrown as given")` was refused as a named
// argument and the whole file failed to compile.
var_dump("thrown as given");
echo "a as b", "\n";
$x = ["k as v" => "v as k"];
var_dump($x);

function pair($first, $second) { return "$first|$second"; }
var_dump(pair(second: "x as y", first: "p as q"));
var_dump(pair("plain as day", second: "also as this"));

// The name itself may contain one too.
function odd($normal) { return $normal; }
var_dump(odd(normal: "ends as this"));
