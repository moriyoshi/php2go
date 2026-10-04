<?php
// A `finally` runs on **every** way out of its try, including the one that is
// easy to miss: an exception no `catch` clause matched. The handler is not
// inside the region it protects -- that is what lets `catch (E) { throw; }`
// escape rather than re-entering its own clause -- so the edge out of the last
// unmatched clause has to be routed back through the finally explicitly.

function f($n) {
    try {
        if ($n === 1) { throw new RuntimeException("one"); }
        if ($n === 2) { return "returned"; }
        if ($n === 3) { throw new LogicException("three"); }
        return "fell through";
    } catch (RuntimeException $e) {
        return "caught " . $e->getMessage();
    } finally {
        echo "  finally $n\n";
    }
}
foreach ([0,1,2,3] as $n) {
    try { var_dump(f($n)); } catch (Throwable $t) { echo "  outer ", get_class($t), ": ", $t->getMessage(), "\n"; }
}
echo "--- finally overrides return\n";
function g() { try { return "try"; } finally { return "finally"; } }
var_dump(g());
echo "--- nested finally\n";
function h() {
    try {
        try { throw new Exception("inner"); }
        finally { echo "  inner finally\n"; }
    } catch (Exception $e) { echo "  caught ", $e->getMessage(), "\n"; }
    finally { echo "  outer finally\n"; }
    return "done";
}
var_dump(h());
echo "--- previous and chaining\n";
try {
    try { throw new Exception("first"); }
    catch (Exception $e) { throw new RuntimeException("second", 5, $e); }
} catch (RuntimeException $e) {
    echo $e->getMessage(), " ", $e->getCode(), " <- ", $e->getPrevious()->getMessage(), "\n";
}
echo "--- multi-catch and finally with break\n";
foreach ([1,2] as $k) {
    try {
        if ($k === 1) throw new TypeError("t");
        throw new ValueError("v");
    } catch (TypeError | ValueError $e) {
        echo "  ", get_class($e), "\n";
    } finally {
        echo "  f$k\n";
    }
}
echo "--- rethrow from finally\n";
function r() {
    try { throw new Exception("original"); }
    finally { echo "  cleanup\n"; }
}
try { r(); } catch (Exception $e) { echo "  ", $e->getMessage(), "\n"; }
