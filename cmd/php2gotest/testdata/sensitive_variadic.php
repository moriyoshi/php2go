<?php
// A `#[SensitiveParameter]` variadic hides every argument it collects in a
// backtrace, not only the first; a plain variadic after a sensitive
// parameter hides nothing of its own.

function shown(array $frame) {
    foreach ($frame['args'] as $i => $a) {
        echo $i, ": ", $a instanceof SensitiveParameterValue ? "hidden(" . $a->getValue() . ")" : var_export($a, true), "\n";
    }
}

function secretRest($open, #[SensitiveParameter] ...$rest) {
    throw new Exception('x');
}
function secretThenRest(#[SensitiveParameter] $key, ...$rest) {
    throw new Exception('y');
}
class Vault {
    function open(#[SensitiveParameter] ...$codes) {
        shown(debug_backtrace()[0]);
    }
}

foreach ([
    fn() => secretRest('a', 'b', 'c', 'd'),
    fn() => secretRest('only'),
    fn() => secretThenRest('k', 'r1', 'r2'),
] as $call) {
    try {
        $call();
    } catch (Exception $e) {
        shown($e->getTrace()[0]);
    }
}
(new Vault)->open(1, 2, 3);
