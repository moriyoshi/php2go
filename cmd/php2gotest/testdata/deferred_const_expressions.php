<?php
// An initializer the compiler could not fold is kept as an **AST**, because it
// names something that may not exist yet. There is no operand text that is the
// value, so the operand indexes the literal instead and the tree renders here.

enum Suit: string {
    case Hearts = 'H';
    case Spades = 'S';
}

// PHP 8.3: a property fetch in a constant expression, allowed on an enum case
// and on nothing else.
const HEARTS_NAME = Suit::Hearts->name;
const HEARTS_VALUE = Suit::Hearts->value;
var_dump(HEARTS_NAME, HEARTS_VALUE);

// `?->` on null yields null and **skips the rest of the chain**, including the
// expression that would have named the next property.
class Printer {
    public function __construct() { echo "Printer\n"; }
}
const SHORT = (null)?->test;
const SHORT_CHAIN = (null)?->test->test;
const SHORT_NAME = (null)?->{new Printer};
var_dump(SHORT, SHORT_CHAIN, SHORT_NAME);

// A property fetch on something that is not an object warns and yields null.
const ON_INT = (1)->test;
var_dump(ON_INT);

// An offset inside a constant expression.
const ELEM = [10, 20, 30][1];
const NESTED = ['a' => ['b' => 'deep']]['a']['b'];
var_dump(ELEM, NESTED);

// The arms of a conditional are not both evaluated: one Printer, not two.
const PICKED = true ? 'yes' : 'no';
const ELVIS = 0 ?: 'fallback';
var_dump(PICKED, ELVIS);

// Unary minus is compiled as `-1 * value`, in that order.
const NEG = -7;
const NEG_FLOAT = -1.5;
var_dump(NEG, NEG_FLOAT);

// `new` in a constant expression, allowed since PHP 8.1 and built each time the
// initializer runs.
class Holder {
    public $seen;
    public function __construct($seen = 'default') { $this->seen = $seen; }
}
const MADE = new Holder('made');
var_dump(MADE->seen);
