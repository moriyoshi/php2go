<?php
// A generator destroyed while suspended is force-closed: its finally blocks
// run where the last holder let it go, a `yield` or `yield from` reached there
// is refused with an Error thrown at it, and a delegate only it holds is
// destroyed first. Refusals the driver detects at a `yield from` are thrown
// inside the body, where the body's own try catches them.

function selfDelegating() {
    try { yield from yield; } catch (Error $e) { echo "caught: ", $e->getMessage(), " @", $e->getLine(), "\n"; yield 5; }
}
$self = selfDelegating();
var_dump($self->send($self));

function notDelegatable() {
    try { yield from 1; } catch (Error $e) { echo "caught: ", $e->getMessage(), " @", $e->getLine(), "\n"; }
}
foreach (notDelegatable() as $x) {}

function notDelegatableUncaught() {
    yield from new stdClass;
}
try {
    foreach (notDelegatableUncaught() as $x) {}
} catch (Error $e) {
    echo $e, "\n";
}

// A warning raised on the line a resumed body stopped at is placed there.
function resumedLine() { $v = yield; echo $v->p, "\n"; }
$resumed = resumedLine();
$resumed->current();
$resumed->send(null);

function yieldInFinally() {
    try {
        echo "try\n";
        yield 1;
    } finally {
        echo "finally\n";
        try {
            yield 2;
        } catch (Error $e) {
            echo "caught: ", $e->getMessage(), " @", $e->getLine(), "\n";
        }
        echo "after\n";
    }
    echo "not reached\n";
}
$closing = yieldInFinally();
$closing->current();
unset($closing);
echo "after unset\n";

function inner() {
    echo "inner\n";
    yield 1;
}
function yieldFromInFinally() {
    try {
        yield from inner();
    } finally {
        echo "finally\n";
        yield from inner();
    }
}
try {
    $refused = yieldFromInFinally();
    $refused->rewind();
    $refused = null;
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), " @", $e->getLine(), "\n";
}

function loopFinally() {
    try {
        yield 1;
    } finally {
        echo "loop finally\n";
        try { yield from [1, 2]; } catch (Error $e) { echo "caught: ", $e->getMessage(), "\n"; }
    }
}
foreach (loopFinally() as $v) {
    break;
}
echo "after loop\n";

// A catch inside a finally keeps the exception the finally is unwinding,
// across a yield.
function catchInFinally() {
    try {
        throw new Exception("A");
    } finally {
        try { yield 1; } catch (Error $e) { echo "caught ", $e->getMessage(), "\n"; }
        echo "after\n";
    }
}
$unwinding = catchInFinally();
var_dump($unwinding->current());
try {
    $unwinding->throw(new Error("E"));
} catch (Exception $x) {
    echo "outer ", $x->getMessage(), "\n";
}

// The delegate only the delegator holds is destroyed first; one held
// elsewhere is left alone.
function owned() {
    try { yield 1; yield 2; } finally { echo "owned finally\n"; }
}
function delegator() {
    try { yield from owned(); } finally { echo "delegator finally\n"; }
}
$outer = delegator();
var_dump($outer->current());
unset($outer);
echo "--\n";
function throwingOwned() {
    try { yield 1; } finally { echo "throwing finally\n"; throw new Exception("inner"); }
}
function throwingDelegator() {
    try { yield from throwingOwned(); } finally { echo "delegator finally\n"; }
}
$throwing = throwingDelegator();
$throwing->current();
try {
    unset($throwing);
} catch (Exception $e) {
    echo "caught ", $e->getMessage(), "\n";
}
echo "--\n";
$held = owned();
function sharing() {
    try { yield from $GLOBALS['held']; } finally { echo "sharing finally\n"; }
}
$sharer = sharing();
$sharer->current();
unset($sharer);
echo "held still open\n";
$held->next();
var_dump($held->current());
$held->next();

// A generator unpacked from a call dies at the unpack.
function badKey() {
    try { yield null => 0; } finally { echo "badKey finally\n"; throw new Exception("from finally"); }
}
try {
    var_dump([...badKey()]);
} catch (Throwable $e) {
    echo get_class($e), " ", $e->getMessage(), " prev=", get_class($e->getPrevious()), "\n";
}

// At shutdown the close is the engine's, traced from an internal frame.
function atShutdown() {
    try { yield 1; } finally { echo "shutdown finally\n"; yield 2; echo "not reached\n"; }
}
$last = atShutdown();
$last->current();
echo "end of script\n";
