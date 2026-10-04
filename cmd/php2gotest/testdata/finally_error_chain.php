<?php
// An error raised inside a finally that interrupted an exception takes that
// exception as its previous, even when nothing in the function catches: a
// return-type refusal, and an error from a called function.

function typed(): array
{
    try {
        throw new Exception("first");
    } finally {
        return null;
    }
}
function raises() { throw new LogicException("from callee"); }
function viaCall()
{
    try {
        throw new RuntimeException("interrupted");
    } finally {
        raises();
    }
}

foreach (['typed', 'viaCall'] as $f) {
    try {
        $f();
    } catch (Throwable $t) {
        for ($p = $t; $p; $p = $p->getPrevious()) {
            echo get_class($p), ": ", $p->getMessage(), "\n";
        }
    }
}
