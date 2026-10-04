<?php
// is_a and is_subclass_of: an object or, when allowed, a class name -- is_a
// takes a string only with `allow_string`, is_subclass_of by default -- and
// the class itself counts for is_a and not for is_subclass_of. A string
// subject is autoloaded; interfaces and the exception hierarchy count.

interface I {}
class P implements I {}
class C extends P {}
spl_autoload_register(function ($n) { echo "autoload $n\n"; });

$c = new C;
foreach ([
    [$c, 'C'], [$c, 'P'], [$c, 'I'], [$c, 'stdClass'],
    ['C', 'P'], ['C', 'C'], ['Nope', 'P'],
    [new RuntimeException, 'Exception'], [new RuntimeException, 'Throwable'],
    [$c, '\P'],
] as [$s, $w]) {
    echo is_string($s) ? $s : get_class($s), " vs $w: ",
        var_export(is_a($s, $w), true), " ",
        var_export(is_a($s, $w, true), true), " ",
        var_export(is_subclass_of($s, $w), true), " ",
        var_export(is_subclass_of($s, $w, false), true), "\n";
}
