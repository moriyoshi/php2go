<?php
// An empty `finally {}` runs nothing: the normal exit carries on, a return
// returns, and an exception keeps unwinding to the enclosing handler.

try {
    echo "try\n";
} finally {
}
function r() { try { return "returned"; } finally { } }
var_dump(r());
function t() {
    try {
        throw new Exception("thrown");
    } finally {
    }
}
try { t(); } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
foreach ([1, 2] as $i) {
    try {
        if ($i == 1) continue;
        echo "loop $i\n";
    } finally {
    }
}
echo "done\n";
