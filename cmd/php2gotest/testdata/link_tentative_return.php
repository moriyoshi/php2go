<?php
// A class the engine links at run time -- anonymous, or declared where
// control reaches it -- is checked against the engine methods it overrides
// whose return types are still tentative: one deprecation per override, at
// the method's line, in the parent's table order.

$a = new class([1]) extends ArrayIterator {
    function current() { return 7; }
};
var_dump($a->current());

if (true) {
    class TwoOverrides extends ArrayIterator
    {
        function key() { return 0; }
        function current() { return 1; }
    }
}

// Compatible or suppressed: nothing to say.
$ok = [
    new class implements IteratorAggregate { function getIterator(): Iterator { return new ArrayIterator([1]); } },
    new class implements IteratorAggregate { function getIterator(): Generator { yield 1; } },
    new class implements IteratorAggregate { function getIterator(): ArrayIterator { return new ArrayIterator([]); } },
    new class([1]) extends ArrayIterator {
        function current(): int { return 1; }
        function valid(): bool { return true; }
        function key(): string|int|null { return 0; }
    },
    new class implements Countable { function count(): int { return 1; } },
    new class implements JsonSerializable { function jsonSerialize(): array { return []; } },
    new class([1]) extends ArrayIterator { #[\ReturnTypeWillChange] function current() { return 5; } },
];
echo count($ok), "\n";

$counted = new class implements Countable { function count() { return 3; } };
echo count($counted), "\n";
$wrong = new class implements Countable { function count(): string { return "4"; } };

// Through a parent of the program's own: the engine's method is what the
// override is checked against.
class Base extends ArrayIterator {}
if (true) {
    class Derived extends Base
    {
        function valid() { return false; }
    }
}

// A handler sees each one with the method's line, called from the
// declaration.
set_error_handler(function ($code, $message, $file, $line) {
    echo "handler($code, line $line): $message\n";
    return true;
});
$d = new class extends DateTime {
    function getTimezone() { return false; }
    function getOffset() { return 0; }
};
echo get_parent_class($d), "\n";

// A handler that throws ends the link with a fatal naming the parent.
set_error_handler(function ($code, $message) {
    throw new Exception("from the handler");
});
$fatal = new class extends DateTime {
    function getTimezone() {}
};
echo "not reached\n";
