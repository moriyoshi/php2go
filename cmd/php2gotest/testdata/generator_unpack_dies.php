<?php
// A generator unpacked straight from a call dies at the unpack and gives its
// handle back. On a failed unpack it dies before the failure travels: what
// its finally throws is what the catch sees, with the failure as previous,
// and the handles come out in the engine's order.
function badKey() {
    try { yield null => 0; } finally { echo "badKey finally\n"; throw new Exception("from finally"); }
}
try {
    var_dump([...badKey()]);
} catch (Throwable $e) {
    echo get_class($e), " ", $e->getMessage(), " #", spl_object_id($e), " prev=",
        get_class($e->getPrevious()), " #", spl_object_id($e->getPrevious()), "\n";
}
function spread() {
    try { yield 1 => 0; } finally { echo "spread finally\n"; }
}
var_dump([...spread()]);
var_dump(new stdClass);
function pair() {
    yield 1;
    yield 2;
}
var_dump([...pair(), ...pair()]);
var_dump(new stdClass);
