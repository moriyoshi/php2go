<?php
// is_callable asks the same question a builtin asks about its callback
// argument, and answers it differently in two places -- both read back from the
// engine: `__call` makes any method name callable, and the third argument is
// written even when the answer is false, including for values that are not
// callables at all.
class C { public function m() {} private function p() {} public static function s() {} public function __call($n, $a) {} }
class E { public function m() {} private function p() {} }
class D { public function __invoke() {} }
$cases = [42, null, [], [1], [1,2,3], [new C,"m"], [new C,"p"], [new C,"nope"],
          [new E,"m"], [new E,"p"], [new E,"nope"], ["C","s"], ["C","m"], ["E","m"],
          ["NoClass","m"], [null,"m"], [new C,5], "C::s", "C::m", "E::nope",
          new D, new stdClass, fn() => 1, "userFn", "nosuchfunction"];
function userFn() {}
foreach ($cases as $i => $c) {
    $name = null;
    $a = is_callable($c) ? 1 : 0;
    $b = is_callable($c, true) ? 1 : 0;
    is_callable($c, false, $name);
    echo $i, " ", $a, $b, " ", ($name === null ? "NULL" : "'" . $name . "'"), "\n";
}
