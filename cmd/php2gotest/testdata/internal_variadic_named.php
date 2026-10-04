<?php
// A library variadic refuses a name it does not declare --
// `array_merge([1], a: [2])` is an ArgumentCountError -- while call_user_func
// forwards the name and a user variadic keeps it under its key.

function f(...$a) { var_dump($a); }
class C { static function m(...$a) { var_dump($a); } }
foreach ([
  fn() => array_merge([1], a: [2]),
  fn() => array_diff_key([1, 2], [3, 4], a: [5, 6]),
  fn() => sprintf("%s", 1, x: 2),
  fn() => max(1, 2, q: 3),
  fn() => call_user_func('f', 1, k: 2),
  fn() => f(1, z: 3),
] as $g) { try { $g(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; } }
