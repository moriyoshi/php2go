<?php
// An internal function's arguments are verified against the engine's declared
// parameter list **before** the function runs. Compiled code skipped that
// entirely, so `str_repeat("a", [])` answered "" instead of raising.
$bad = [[], new stdClass, fn() => 1];
foreach ($bad as $v) {
    try { var_dump(str_repeat("a", $v)); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
    try { var_dump(implode(",", $v)); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
    try { var_dump(in_array(1, $v)); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
    try { var_dump(array_keys($v)); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
}
// Coercion, not refusal: a numeric string and a bool are converted the way the
// engine converts them, and the callee sees the converted value.
var_dump(str_repeat("a", "3"), str_repeat("a", true), str_repeat("ab", 0));
var_dump(implode(",", [1, 2]), in_array("1", [1]));
// Passing null to a scalar parameter is deprecated and then coerced to the
// type's zero -- it is not an error.
var_dump(str_repeat("a", null));
// A by-reference parameter is not checked here: the native implementation
// parses its own.
$arr = [3, 1, 2];
var_dump(sort($arr), $arr);
