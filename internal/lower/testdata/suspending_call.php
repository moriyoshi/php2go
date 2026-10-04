<?php
namespace App;

use Fiber;

// The callee: it suspends, so its frame is a state machine.
function inner($v): string
{
    return Fiber::suspend($v) . "!";
}

// The caller. `inner()` can give control up from inside this frame, so this
// frame has to be able to as well -- which is the whole caller question. The
// call is what has to become a suspension point; everything after it is the
// continuation, resumed with `inner`'s **return value**.
function outer($v): string
{
    $before = strlen($v);
    $got = inner($v);
    return $before . $got;
}

// Two in one block, and one whose result nothing reads: both still have to
// split, since the resumption has to be marked either way.
function twice($v): string
{
    inner($v);
    $b = inner($v);
    return $b;
}
