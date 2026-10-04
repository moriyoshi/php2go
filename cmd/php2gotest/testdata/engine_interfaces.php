<?php
// The interfaces the engine provides and attaches implicitly. No program
// declares them, so the "is this class declared here" gate would refuse every
// `instanceof` naming one -- and answering false instead would be worse, since
// it silently declines a test PHP passes.

enum Backed: string { case A = 'a'; }
enum Pure { case B; }
$closure = function () { return 1; };
$arrow = fn() => 2;
class Speaks { public function __toString(): string { return "spoken"; } }
class Silent {}

// A **pure** enum is not a BackedEnum. That is the whole distinction the two
// interfaces draw, so the test is whether the case carries a value.
var_dump(Backed::A instanceof UnitEnum, Backed::A instanceof BackedEnum);
var_dump(Pure::B instanceof UnitEnum, Pure::B instanceof BackedEnum);

var_dump($closure instanceof Closure, $arrow instanceof Closure);
var_dump($closure instanceof Stringable, $closure instanceof UnitEnum);

// Stringable is attached to anything declaring `__toString`, including the
// engine's own exceptions.
var_dump(new Speaks instanceof Stringable, new Silent instanceof Stringable);
var_dump(new Exception("x") instanceof Stringable, new Exception("x") instanceof Throwable);
var_dump(new Silent instanceof Closure, Backed::A instanceof Closure);

// A first-class callable is a Closure too.
function named(): int { return 3; }
$fc = named(...);
var_dump($fc instanceof Closure);

// And the ordinary declared hierarchy still answers as before.
interface Own {}
class Implements1 implements Own {}
var_dump(new Implements1 instanceof Own, new Implements1 instanceof Stringable);
