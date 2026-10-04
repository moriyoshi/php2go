<?php
// A function declared inside another is declared each time that one runs,
// and the second time is a fatal error naming where it was first declared;
// function_exists() tells the first call it is there.

register_shutdown_function(function () { echo "shutdown\n"; });
function outer() {
    function inner() { return "inner"; }
}
var_dump(function_exists('inner'));
outer();
var_dump(function_exists('inner'), inner());
try {
    outer();
} catch (Throwable $t) {
    echo "caught\n";
}
echo "never\n";
