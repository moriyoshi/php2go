<?php
// in_array over a constant haystack compiles to its own opcode, which looks
// a string needle up exactly -- "1e0" does not find "1", though "1e0" ==
// "1" -- and compares anything else loosely unless strict.
$s = "1";
var_dump(in_array($s, [1, 2], true));
var_dump(in_array($s, ["1", "b"]), in_array($s, ["1", "b"], true));
var_dump(in_array("1e0", ["1", "b"]), in_array("1e0", [1, 2]));
var_dump(in_array(1, ["1", "a"]), in_array(1, ["1", "a"], true));
var_dump(in_array(0, ["a", "b"]), in_array(null, ["", "a"]), in_array(false, ["", "a"]));
var_dump(in_array(1.0, ["1", "x"]), in_array(true, ["x"]));
var_dump(in_array(2, [1, 2], true), in_array("2", [1, 2], true));
var_dump(in_array($undefined, ["", "z"]));
