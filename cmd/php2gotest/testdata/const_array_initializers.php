<?php
// A constant array literal is not a Go composite literal: appending can fail,
// a key is separate from its presence, and an element can be a spread.

const NUMS = [1, 2, 3];
const KEYED = ["a" => 1, 5 => "five", "b" => 2];
const NESTED = [[1, 2], ["k" => [3]]];
const SPREAD = [0, ...NUMS, 9];
const SPREAD_KEYED = ["x" => 1, ...KEYED];
var_dump(NUMS, KEYED, NESTED, SPREAD, SPREAD_KEYED);

// The key is present and null, which is not the same as having no key.
const NULL_KEY = [null => "n", "after"];
var_dump(NULL_KEY);

function withDefault($a = ["p" => PHP_INT_SIZE], $b = [1 => "one"]) {
    var_dump($a, $b);
}
withDefault();

class Holder {
    public $items = ["first", "k" => "second"];
    const TABLE = [1, 2, 3];
}
$h = new Holder;
var_dump($h->items, Holder::TABLE);

// An undefined constant in a deferred initializer **throws**, and it throws
// when the initializer runs -- not when it is compiled, and not at all if the
// argument is supplied.
function optional($a = MISSING_CONSTANT) { return $a; }
var_dump(optional("given"));
try {
    optional();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

// The position a diagnostic names is the line the initializer was **written**
// on, not the line that read it.
try {
    optional();
} catch (Error $e) {
    echo "line ", $e->getLine(), "\n";
}
