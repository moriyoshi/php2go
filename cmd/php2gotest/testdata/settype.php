<?php
// settype is the casts written as a function, and the one library function that
// writes **through a reference** the caller owns.
foreach ([["bool", 1], ["boolean", "0"], ["int", "12abc"], ["integer", 2.9],
          ["float", "1.5"], ["double", 3], ["string", 42], ["array", "x"],
          ["array", null], ["null", "gone"]] as [$type, $value]) {
    $held = $value;
    var_dump(settype($held, $type), $held);
}

// To object: an array becomes properties, a scalar goes under `scalar`, and an
// object is left alone. Done **before** anything throws, because a caught
// exception is an object too and the handle numbers are graded.
$a = ["x" => 1, 2 => "two"];
var_dump(settype($a, "object"), $a);
$n = 5;
var_dump(settype($n, "object"), $n);

// An object with __toString converts; one without is an Error.
class Rendered { public function __toString(): string { return "rendered"; } }
$o = new Rendered;
var_dump(settype($o, "string"), $o);
$already = new Rendered;
var_dump(settype($already, "object"), get_class($already));
$bare = new stdClass;
try {
    settype($bare, "string");
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}

// There is no conversion *to* a resource, and an unknown name is a ValueError.
$r = 1;
try { settype($r, "resource"); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
try { settype($r, "nonsense"); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
