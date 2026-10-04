<?php
// A value joined at a merge from an untyped parameter and a number is not a
// number: the parameter may hold anything. Inference once read the untyped
// side as contributing nothing, which proved the join a float -- skipping the
// operand check a string or an array owes, and letting an unboxed float round
// an int.

function joined($n, $flip)
{
    $x = $n;
    if ($flip) {
        $x = 1.5;
    }
    try {
        var_dump($x + 1);
    } catch (TypeError $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
joined("abc", false);
joined([1], false);
joined(PHP_INT_MAX, false);
joined("5", true);

function bound($n)
{
    $steps = 0;
    for ($i = PHP_INT_MAX - 3; $i < $n; $i++) {
        if ($i == PHP_INT_MAX - 2) {
            $n = 1e19;
        }
        if (++$steps > 4) {
            break;
        }
        var_dump($i);
    }
    var_dump($n);
}
bound(PHP_INT_MAX);

// An undefined constant and an untracked variable are unknown too.
function viaGlobal($flip)
{
    global $g;
    $x = $g;
    if ($flip) {
        $x = 2.5;
    }
    try {
        var_dump($x * 2);
    } catch (TypeError $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
$g = "text";
viaGlobal(false);
$g = PHP_INT_MAX;
viaGlobal(false);
