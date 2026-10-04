<?php
// A by-value parameter that only reads its array hands its share back when the
// call returns, so the caller writes in place again (OWNERSHIP-PERF.md P3b). Each
// shape here is one where handing it back wrongly would let the caller's write
// reach something that must keep the old array -- or the reverse.

function total($a)
{
    return count($a);
}

// Read-only, called each time round a growing loop: the shape the release is
// for. Only the arithmetic is observable; the point is that it stays right.
function grow($n)
{
    $arr = [];
    $sum = 0;
    for ($i = 0; $i < $n; $i++) {
        $arr[] = $i;
        $sum += total($arr);
    }
    return [$sum, count($arr)];
}

// Returned: the caller holds the very header, so it is not handed back. A
// by-reference loop over the result must not reach the caller's array.
function id($a)
{
    return $a;
}

function iterateReturned()
{
    $mine = [1, 2];
    foreach (id($mine) as &$v) {
        $v = 0;
    }
    unset($v);
    return $mine;
}

// Kept in a global, a property and a static by the callee, then the caller
// writes: each keeper holds its own share and keeps the old array.
function keepGlobal($a)
{
    global $kept;
    $kept = $a;
    return count($a);
}

class Keeper
{
    public $held;
    public static $stat;
    public function keep($a)
    {
        $this->held = $a;
        return count($a);
    }
    public static function keepStatic($a)
    {
        self::$stat = $a;
        return count($a);
    }
}

function passOn($a)
{
    return keepGlobal($a);
}

function keepers()
{
    $mine = [1];
    keepGlobal($mine);
    $mine[] = 2;
    global $kept;
    $a = count($kept);

    $k = new Keeper();
    $k->keep($mine);
    $mine[] = 3;
    $b = count($k->held);

    Keeper::keepStatic($mine);
    $mine[] = 4;
    $c = count(Keeper::$stat);

    passOn($mine);
    $mine[] = 5;
    $d = count($kept);
    return [$a, $b, $c, $d, count($mine)];
}

// The callee writes its own copy; the caller's array is untouched, and the
// caller's own write afterwards lands in its array.
function scribble($a)
{
    $a[] = "x";
    return count($a);
}

function callerAfterCalleeWrote()
{
    $mine = [1];
    $n = scribble($mine);
    $mine[] = 2;
    return [$n, $mine];
}

// A local copy hands its share back after its last use -- not before. Each of
// these reads the copy after the source changed, or keeps it past that point.
function copyReadAfterWrite()
{
    $arr = [1];
    $copy = $arr;
    $arr[] = 2;
    return [count($copy), count($arr)];
}

function copyIteratedWhileGrowing()
{
    $arr = [1, 2];
    $copy = $arr;
    $seen = 0;
    foreach ($copy as $v) {
        $arr[] = $v;
        $seen++;
    }
    return [$seen, count($arr)];
}

function copyStoredThenSourceWritten()
{
    $arr = [1];
    $box = [];
    $copy = $arr;
    $box[] = $copy;
    $arr[] = 2;
    return [count($box[0]), count($arr)];
}

function copyReturned()
{
    $arr = [1];
    $copy = $arr;
    return $copy;
}

function copyPassedThenSourceWritten()
{
    $arr = [1];
    $copy = $arr;
    $n = total($copy);
    $arr[] = 2;
    $again = $arr;
    $arr[] = 3;
    return [$n, count($again), count($arr)];
}

function copyLoop($n)
{
    $arr = [];
    $sum = 0;
    for ($i = 0; $i < $n; $i++) {
        $arr[] = $i;
        $copy = $arr;
        $sum += count($copy);
    }
    return $sum;
}

var_dump(copyReadAfterWrite());
var_dump(copyIteratedWhileGrowing());
var_dump(copyStoredThenSourceWritten());
$r = copyReturned();
$r[] = 9;
var_dump($r, copyReturned());
var_dump(copyPassedThenSourceWritten());
var_dump(copyLoop(30));
var_dump(grow(50));
var_dump(iterateReturned());
var_dump(keepers());
var_dump(callerAfterCalleeWrote());
