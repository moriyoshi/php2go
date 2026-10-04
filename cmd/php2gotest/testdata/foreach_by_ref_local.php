<?php
// `foreach ($a as &$v)` writes through `$a`'s storage box, so `$a` cannot be
// promoted to an SSA value: the promotion pass read the loop's argument as a
// value, let `$a` go, and `return $a` answered the array from before the loop.
//
// Each loop ends with `unset($v)`: while `$v` still references the last element,
// `var_dump` marks it `&`, which compiled output cannot yet decide the way the
// engine does (a reference count question, in LTM object-lifetime).
function literal()
{
    $a = [1, 2];
    foreach ($a as &$v) {
        $v *= 10;
    }
    unset($v);
    return $a;
}

function built($x)
{
    $a = [$x, $x + 1];
    foreach ($a as &$v) {
        $v = -$v;
    }
    unset($v);
    return $a;
}

function appended()
{
    $a = ["a"];
    foreach ($a as $k => &$v) {
        $v .= "!";
    }
    unset($v);
    $a[] = "b";
    return $a;
}

var_dump(literal());
var_dump(built(3));
var_dump(appended());
