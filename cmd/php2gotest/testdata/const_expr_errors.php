<?php
// A deferred property initializer cannot return an error -- it renders as
// `func(*Ctx) Value` and a constructor answers with an object -- so a failure
// inside one is **parked** and collected at the next instruction boundary.
// Swallowing it made the object materialise with a null where PHP stops.
//
// And the class is resolved **before** the constant is looked up: only the
// combination where the class exists reports the constant. Asked of the engine
// for all three.

class Known
{
    const X = 1;
}

class BadClass
{
    public int $bar = NOPE::BAZ;
}

class BadConst
{
    public int $bar = Known::MISSING;
}

try {
    new BadClass();
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}

try {
    new BadConst();
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}

// Directly, not through an initializer: the same two answers.
try {
    NOPE::BAZ;
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    Known::MISSING;
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}

// A sound initializer still works, and the program carries on.
class Good
{
    public int $bar = Known::X;
}
var_dump((new Good)->bar);
echo "still running\n";
