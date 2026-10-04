<?php
// Under zend.exception_ignore_args an exception's trace keeps no arguments:
// the frame still names the call, with nothing between the parentheses, and
// getTrace() has no `args` key. The setting is read when the exception is
// made (exception_ignore_args).

function f($secret, $other) {
    return new Exception("x");
}

$before = f("pw", 1);
ini_set("zend.exception_ignore_args", 1);
$after = f("pw", 2);
echo $before->getTraceAsString(), "\n";
echo $after->getTraceAsString(), "\n";
var_dump(array_key_exists("args", $after->getTrace()[0]), array_key_exists("args", $before->getTrace()[0]));
function g($secret) {
    strlen();
}
try {
    g("pw");
} catch (ArgumentCountError $e) {
    echo $e->getTraceAsString(), "\n";
}
ini_set("zend.exception_ignore_args", 0);
echo f("pw", 3)->getTraceAsString(), "\n";
ini_set("zend.exception_ignore_args", 1);
function h($secret, $d) {
    return 1 % $d;
}
try {
    h("pw", 0);
} catch (DivisionByZeroError $e) {
    echo $e->getTraceAsString(), "\n";
}
