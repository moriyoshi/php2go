<?php
// A builtin checks its callback argument **before** calling it, and says
// something different from what the call would: an unusable callback is an
// argument error naming the parameter, where invoking the same value as `$f()`
// is `Call to undefined function`. Compiled code answered the second for both.
//
// Every reason below was read back from the engine one shape at a time.
function userCallback($carry, $item) { return $carry; }
class C { public function m() {} private function p() {} public static function s() {} }
$cases = [
  "nosuchfunction", 42, null, true, 1.5, [], [1], [1,2,3],
  [new C, "nope"], ["C", "nope"], [new C, "p"], ["C", "m"], ["NoClass", "m"],
  [null, "m"], [new C, 5], ["C::m"], "C::nope", "C::m", new stdClass,
  fn($c, $v) => $c, "userCallback", [new C, "m"], ["C", "s"], "C::s",
];
foreach ($cases as $i => $c) {
    try { array_reduce([1], $c); echo "$i: ok\n"; }
    catch (Throwable $e) { echo "$i: ", $e->getMessage(), "\n"; }
}
