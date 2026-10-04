<?php
// A `catch` body is **not** inside the region it belongs to -- that is what
// lets `catch (E) { throw; }` escape its own clause -- but the region's
// `finally` still runs on the way out. Both halves came from the engine.

function rethrows() {
    try {
        throw new Exception("t");
    } catch (Exception $e) {
        echo "catch\n";
        throw $e;
    } finally {
        echo "fin\n";
        return "R";
    }
}
echo "return in finally discards: ";
try { var_dump(rethrows()); } catch (Throwable $e) { echo "caught ", $e->getMessage(), "\n"; }

function propagates() {
    try {
        throw new Exception("t");
    } catch (Exception $e) {
        throw new Exception("from catch");
    } finally {
        echo "fin2\n";
    }
}
echo "no return in finally: ";
try { propagates(); } catch (Throwable $e) { echo "caught ", $e->getMessage(), "\n"; }

// A raise from a **called function** inside the exceptional copy chains the
// interrupted exception, exactly as a literal `throw` there does.
function boom($msg) { throw new Exception($msg); }
function chained() {
    try {
        try {
            boom("try");
        } catch (Exception $e) {
            boom("catch");
        } finally {
            boom("finally");
        }
    } catch (Exception $e) {
        return $e;
    }
}
$ex = chained();
do {
    var_dump($ex->getMessage());
} while ($ex = $ex->getPrevious());

// The ordinary finally paths, unchanged.
function retInTry() { try { return "T"; } finally { return "F"; } }
var_dump(retInTry());
function loopBreak() { foreach ([1, 2, 3] as $v) { try { if ($v == 2) break; } finally { echo "bf$v\n"; } } return "done"; }
var_dump(loopBreak());
function nested() { try { try { throw new Exception("in"); } finally { echo "inner fin\n"; } } catch (Exception $e) { return $e->getMessage(); } }
var_dump(nested());
