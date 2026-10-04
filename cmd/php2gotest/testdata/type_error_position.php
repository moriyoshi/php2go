<?php
// Where a coercion TypeError says it happened, and what its first trace frame
// names. These are two different facts -- the position is where the failure
// occurred, the frame is what was being called -- and reading both after the
// callee's frame is popped collapses them into one wrong answer that still
// looks self-consistent.
//
// The frame also prints the **declared** spelling of the function. Zend
// lowercases a function-name literal at the call site, so taking the name from
// there gave `wantsarray()`.

function wantsArray(): array
{
    return null;
}

function wantsInt(int $n): int
{
    return $n;
}

class Holder
{
    public function give(): stdClass
    {
        return 'nope';
    }
}

function report(\TypeError $e): void
{
    echo $e->getMessage(), "\n";
    echo 'at line ', $e->getLine(), "\n";
    echo $e->getTraceAsString(), "\n";
}

try {
    wantsArray();
} catch (\TypeError $e) {
    report($e);
}

try {
    wantsInt('not a number');
} catch (\TypeError $e) {
    report($e);
}

try {
    (new Holder)->give();
} catch (\TypeError $e) {
    report($e);
}
