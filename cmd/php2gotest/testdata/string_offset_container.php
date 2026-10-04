<?php
// A string offset cannot be the container of another fetch, and the refusal
// is worded by what the next fetch is -- `as an array` for a dimension (write,
// read-write, step, unset, append or reference alike), `as an object` for a
// property -- while `&$s[0]` itself is the reference refusal and `$s[] op=` is
// the append refusal, raised before the right side is combined.

$cases = [
 'W dim' => function () { $x = "abc"; $x[0][1] = "z"; },
 'W dim str key' => function () { $x = "abc"; $x["2x"]["y"] = "z"; },
 'RW dim' => function () { $x = "abc"; $x[0]["y"] .= "z"; },
 'W obj' => function () { $x = "abc"; $x[0]->p = 1; },
 'RW obj' => function () { $x = "abc"; $x[0]->p .= 1; },
 'incdec dim' => function () { $x = "abc"; $x[0]["y"]++; },
 'unset nested' => function () { $x = "abc"; unset($x[0][1]); },
 'append nested' => function () { $x = "abc"; $x[0][] = 1; },
 'ref nested' => function () { $x = "abc"; $r = &$x[0][1]; },
 'ref direct' => function () { $x = "abc"; $r = &$x[0]; },
 'append op' => function () { $x = "abc"; $x[] .= "q"; },
 'append op str' => function () { $str = "abc"; $str[] += $str; },
];
foreach ($cases as $k => $f) { try { $f(); echo "$k: ok\n"; } catch (Throwable $e) { echo "$k: ", get_class($e), ": ", $e->getMessage(), "\n"; } }
