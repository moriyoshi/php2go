<?php
// A computed variable name is converted to a string the way any conversion
// is: an object without __toString is an Error, one with it names the
// variable, and an int names its decimal spelling.

class N { function __toString() { return "named"; } }
try { global ${new stdClass}; } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { $x = ${new stdClass}; } catch (Error $e) { echo $e->getMessage(), "\n"; }
${new N} = 5;
var_dump($named);
${1} = "one";
var_dump(${"1"});
