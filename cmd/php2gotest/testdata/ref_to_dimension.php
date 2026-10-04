<?php
// Taking a reference into a dimension of a temporary is silent for some
// containers and `Cannot use temporary expression in write context` for others,
// and the split is **not** something the shape of the IR gives away: a property
// read is an intrinsic call and is storage-bearing, while `func_get_args()` is
// also an intrinsic call and is not.
//
// Measured across eight producers rather than guessed from the three that were
// reported. Silent: a method call, a function call, a property read, a static
// property read, a plain local. An Error: a const array, `func_get_args()`, an
// array literal -- and the direct spelling of those three is a *compile-time*
// error, so they are unreachable from here and keep the refusal.
//
// `separate` sits between the producer and the consumer and carries the
// property through; without that the call cases stayed refused.
const C = [1, 2];
class M { public $arr = [1,2]; public static $sarr = [1,2]; public function m() { return [1,2]; } }
function f() { return [1, 2]; }
function byRef(&$r) { $r = 99; }
$m = new M();
function probe($label, $fn) { try { $fn(); echo "$label: silent\n"; } catch (Throwable $e) { echo "$label: ", $e->getMessage(), "\n"; } }
probe("method call",   function () use ($m) { byRef($m->m()[0]); });
probe("function call", function () { byRef(f()[0]); });
probe("property",      function () use ($m) { byRef($m->arr[0]); });
probe("static prop",   function () { byRef(M::$sarr[0]); });
probe("plain local",   function () { $l = [1,2]; byRef($l[0]); });
var_dump($m->arr, M::$sarr);
