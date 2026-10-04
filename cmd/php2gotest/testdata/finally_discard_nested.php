<?php
// A return leaving a finally discards only that finally's exception: one
// nested in another finally's unwind leaves the outer one in flight
// until the finallys it passes through have run (try/bug70228_3).
function chain(Throwable $e) { $out = []; do { $out[] = $e->getMessage(); } while ($e = $e->getPrevious()); return implode(" <- ", $out); }
function a() { try { throw new Exception("1"); } finally { return "a-ret"; } }
function b() { try { throw new Exception("1"); } finally { try { } finally { return "b-ret"; } } }
function c() {
    try { throw new Exception("1"); }
    finally { try { try { } finally { return 42; } } finally { throw new Exception("2"); } }
}
function d() {
    try { throw new Exception("1"); }
    finally { foreach ([1, 2] as $i) { try { continue; } finally { echo "inner $i\n"; } } }
}
function e() {
    try { return "e-ret"; }
    finally { try { try { } finally { return 43; } } finally { echo "e-mid\n"; } }
}
function f() {
    try { throw new Exception("1"); }
    finally { try { throw new Exception("2"); } catch (Exception $x) { echo "caught ", $x->getMessage(), "\n"; } finally { return "f-ret"; } }
}
foreach (['a', 'b', 'c', 'd', 'e', 'f'] as $fn) {
    try { var_dump($fn()); } catch (Exception $x) { echo "$fn threw: ", chain($x), "\n"; }
}
