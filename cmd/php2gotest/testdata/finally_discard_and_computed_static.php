<?php
// A `finally` that leaves by another route **abandons what was in flight**.
// The engine keeps the in-flight exception in the FAST_CALL's temporary and
// discards it; here the finally is inlined, so the unwind copy is the one
// carrying it.

function returnsFromFinally() {
    try {
        throw new Exception("never seen");
    } finally {
        return "finally wins";
    }
}
var_dump(returnsFromFinally());

// A finally that throws replaces the exception and chains the old one.
function throwsFromFinally() {
    try { throw new Exception("first"); } finally { throw new Exception("second"); }
}
try {
    throwsFromFinally();
} catch (Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), " previous=", $e->getPrevious()->getMessage(), "\n";
}

// A finally with no early exit still rethrows.
function plainFinally() {
    try { throw new Exception("kept"); } finally { echo "cleanup\n"; }
}
try { plainFinally(); } catch (Throwable $e) { echo "caught ", $e->getMessage(), "\n"; }

// A `finally` **nested inside another finally**. The outer copy's blocks sit,
// by opline position, inside the inner try's region, so "the handler outside my
// region" walks back to the copy itself unless the copy also records which
// finally it *is*.
function nested() {
    try {
        throw new Exception("outer");
    } finally {
        try {
            throw new Exception("inner");
        } finally {
            return 42;
        }
    }
}
var_dump(nested());

// `static $x = <not a constant>` is computed on **first entry** and consulted
// on every later one: the compiler could not fold it, so the initializer is in
// the body with a jump past it.
class Box { public $n = 0; }
function counter() {
    static $calls = 0;
    static $box = new Box;
    static $made = [1, 2, 3];
    $calls++;
    $box->n = $calls;
    return [$calls, $box->n, count($made)];
}
var_dump(counter(), counter(), counter());

// The static is bound by reference, so assigning the local is assigning it.
function replaces() {
    static $held = new Box;
    $held->n++;
    return $held->n;
}
var_dump(replaces(), replaces());
