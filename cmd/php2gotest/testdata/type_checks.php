<?php
// `=== null` and `!== null` are ONE opcode with opposite masks: 2 and 1020.
$a = null;
var_dump($a === null, $a !== null);
$b = 1;
var_dump($b === null, $b !== null);
// Every predicate the compiler turns into the same opcode.
foreach ([null, false, true, 1, 1.5, "s", [1], new stdClass] as $v) {
    var_dump(is_null($v), is_bool($v), is_int($v), is_float($v),
             is_string($v), is_array($v), is_object($v), is_scalar($v));
}
// `=== true` and `=== false` are masks too, not comparisons.
var_dump(true === true, 1 === true, false === false, 0 === false);
// An undefined variable warns and then tests as null.
var_dump($undef === null);
echo "done\n";
