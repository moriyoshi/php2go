<?php
// A compiled program can buffer its own output now: the ob_* family moved to
// runtime/stdlib, and the one thing it needed from the interpreter -- running
// a PHP handler -- comes through the Ctx call hook.
ob_start();
echo "captured";
$held = ob_get_clean();
var_dump($held);

// A handler is a PHP callable, and it is told a phase word: 8 is final, |2 a
// clean, |1 the handler's first run. What a flush writes is what the handler
// returned; what ob_get_clean answers is the raw buffer.
ob_start(function ($text, $phase) { return "[$phase:$text]"; });
echo "x";
ob_end_flush();
echo "\n";

ob_start(function ($text, $phase) { return strtoupper($text); });
echo "y";
var_dump(ob_get_flush());
echo "\n";

// Returning false means "use what I was given".
ob_start(function ($text, $phase) { return false; });
echo "declined";
ob_end_flush();
echo "\n";

// A chunk size is not a hint: the handler runs each time the buffer holds that
// much, and the program can count the calls.
$runs = 0;
ob_start(function ($text, $phase) use (&$runs) { $runs++; return $text; }, 4);
echo "abcdef";
ob_end_flush();
echo "|", $runs, "\n";

// Nesting: the inner buffer takes the writes, and the outer one sees only what
// escapes it.
ob_start();
echo "outer:";
ob_start();
echo "inner";
$inner = ob_get_clean();
$outer = ob_get_clean();
var_dump($inner, $outer);

// A bad handler is not a TypeError. It warns with the reason, adds a notice,
// and answers false with no buffer opened.
var_dump(ob_start("no_such_handler_here"));
var_dump(ob_start(42));

// A handler that throws: what it was given is written out unchanged, and the
// throw carries on.
ob_start(function ($text, $phase) { throw new Exception("boom"); });
echo "held";
try {
    ob_end_flush();
} catch (Exception $e) {
    echo "|caught:", $e->getMessage(), "\n";
}
