<?php
// Array literals the compiler builds once and shares, and copies it does not
// make (OWNERSHIP-PERF.md P3a). Each shape is one where sharing would print
// something different if the proof behind it were wrong: a write that must
// land in a copy, a value that must not leave the frame, per-array state that
// must not carry over from one evaluation to the next.

// Read-only in a loop: the shared shape.
function readOnly($n)
{
    $last = null;
    for ($i = 0; $i < $n; $i++) {
        $x = [1, 2, 3] ?: null;
        $last = $x;
    }
    return count($last) . ":" . count([4, 5]);
}

// Written on one iteration only: every evaluation must start from the literal.
function writtenOnce($n)
{
    $out = [];
    for ($i = 0; $i < $n; $i++) {
        $a = [1, 2];
        if ($i == 1) {
            $a[] = 3;
        }
        $out[] = count($a);
    }
    return $out;
}

// Returned: the caller's write must not reach the next call's value.
function lit()
{
    return [1, 2];
}

// Handed through a callee that gives it back, then sorted by the caller.
function id($a)
{
    return $a;
}

function sortedCopy()
{
    $r = id([3, 1, 2]);
    sort($r);
    return [$r, id([3, 1, 2])];
}

// The internal pointer is per array: `next` on one evaluation must not be seen
// by the next.
function pointer()
{
    $a = ["x", "y", "z"];
    next($a);
    return current($a);
}

// Iterated by value, then asked where its pointer is.
function iterated()
{
    $a = [1, 2, 3];
    $sum = 0;
    foreach ($a as $v) {
        $sum += $v;
    }
    return $sum . "/" . current($a);
}

// Sorted in a loop, from the same literal each time.
function sortedInLoop($n)
{
    $firsts = [];
    for ($i = 0; $i < $n; $i++) {
        $a = [3, 1, 2];
        sort($a);
        $firsts[] = $a[0];
        $a[] = 0;
    }
    return $firsts;
}

// Stored into a property that is then written.
class Box
{
    public $items;
}

function viaProperty()
{
    $b = new Box();
    $b->items = [1, 2];
    $b->items[] = 3;
    return [count($b->items), count([1, 2])];
}

// The same literal text in a reader and in a writer.
function reader()
{
    return count(["same", "text"]);
}

function writer()
{
    $a = ["same", "text"];
    $a[] = "more";
    return count($a);
}

// Iterated by reference with no variable in between: the loop writes the
// array itself, so an evaluation shared with the next would see the writes.
function refLoops($n)
{
    for ($i = 0; $i < $n; $i++) {
        foreach ([1, 2] as &$v) {
            echo $v, " ";
            $v *= 10;
        }
        unset($v);
    }
    echo "\n";
}

// The same through a call's result, which leaves the callee's frame.
function refLoopOverCall($n)
{
    for ($i = 0; $i < $n; $i++) {
        foreach (lit() as &$v) {
            echo $v, " ";
            $v *= 10;
        }
        unset($v);
    }
    echo "\n";
}

// A ternary's merge stores the literal it chose; writing the result must land
// in a copy, every time round.
function mergedThenWritten($n)
{
    $out = [];
    for ($i = 0; $i < $n; $i++) {
        $x = $i % 2 ? [1] : [2, 2];
        $x[] = $i;
        $out[] = count($x);
    }
    return $out;
}

function mergedThenReturned($f)
{
    $x = $f ? [1] : [2, 2];
    return $x;
}

function mergedReturnedAndWritten()
{
    $a = mergedThenReturned(true);
    $a[] = 9;
    return [count($a), count(mergedThenReturned(true))];
}

var_dump(mergedThenWritten(4));
var_dump(mergedReturnedAndWritten());
refLoops(3);
refLoopOverCall(3);
var_dump(readOnly(3));
var_dump(writtenOnce(3));
$a = lit();
$a[] = 3;
var_dump(count($a), lit());
var_dump(sortedCopy());
var_dump(pointer(), pointer());
var_dump(iterated(), iterated());
var_dump(sortedInLoop(3));
var_dump(viaProperty(), viaProperty());
var_dump(writer(), reader(), writer(), reader());
