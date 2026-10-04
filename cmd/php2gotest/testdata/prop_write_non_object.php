<?php
// Writing a property of a non-object is a catchable **Error**, not a Go error
// escaping the program -- and the receiver is fetched *for write*, so an
// undefined variable earns no `Undefined variable` warning on the way.
//
// Reading and writing are asymmetric here: a read warns and yields null, a
// write throws. Both halves are exercised so the asymmetry is pinned.

foreach ([null, 1, 's', true, false, 1.5, []] as $v) {
    $x = $v;
    try {
        $x->p = 1;
        echo "assigned\n";
    } catch (\Error $e) {
        echo $e->getMessage(), "\n";
    }
}

// The undefined variable: an Error and nothing else.
try {
    $neverSet->q = 1;
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}

// The read side does warn, and yields null.
$alsoNeverSet = null;
var_dump($alsoNeverSet->r);

// A real object still works, and the program is still running.
$o = new stdClass;
$o->ok = 'yes';
var_dump($o->ok);
echo "still running\n";
