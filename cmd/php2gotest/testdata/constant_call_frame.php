<?php
// An error `constant()` raises is thrown from inside it: the trace's first
// frame is the `constant(...)` call, with its argument (traits/constant_018).

trait TestTrait {
    public const Constant = 42;
}
class K {
    private const HIDDEN = 1;
}

foreach (['TestTrait::Constant', 'K::NOPE', 'NOPE', 'K::HIDDEN', 'Missing::X'] as $name) {
    try {
        var_dump(constant($name));
    } catch (Error $e) {
        $frame = $e->getTrace()[0];
        echo get_class($e), ": ", $e->getMessage(), " | ", $frame['function'], "(", $frame['args'][0], ") line ", $frame['line'], "\n";
    }
}
var_dump(defined('K::NOPE'), constant('PHP_EOL') === "\n");
