<?php
// A call to a function not yet declared where it is written -- recursion, or a
// forward call -- is an undecided send (SEND_VAR_EX) in the engine's bytecode.
// Compiled output resolves the callee and passes a by-value parameter the
// value rather than a reference box (OWNERSHIP-PERF.md). Each shape here is
// one where that must not happen, or must still behave as the engine does.

function countdown($n)
{
    if ($n == 0) {
        return 0;
    }
    $m = $n - 1;
    return 1 + countdown($m);
}
var_dump(countdown(5));

// By reference: the recursive call must still bind.
function fill(&$acc, $n)
{
    if ($n == 0) {
        return;
    }
    $acc[] = $n;
    fill($acc, $n - 1);
}
$list = [];
fill($list, 3);
var_dump($list);

// Forward calls, before the declarations below are reached.
$x = 1;
laterByRef($x);
var_dump($x);
$y = 5;
var_dump(laterByVal($y), $y);

// An undefined local sent by value warns at the send and arrives as null.
function probe()
{
    return laterByVal($nope);
}
var_dump(probe());

// Variadic, unpack and named: the positions are not the parameters'.
$a = 1;
$b = 2;
var_dump(laterVariadic($a, $b));
$args = [10];
var_dump(laterTwo(...$args, second: $b));
var_dump(laterTwo(second: $a, first: $b));

function laterByRef(&$r) { $r = "changed"; }
function laterByVal($v) { $v = "local"; return $v; }
function laterVariadic(...$all) { $all[] = "x"; return $all; }
function laterTwo($first, $second) { return [$first, $second]; }

// Settled while lowering, a by-value send lets the local become an SSA value
// (lower.settleSends). Value semantics must survive that: the callee's writes
// to its copy of an array never reach the caller's, an object is still the
// same handle, and one local sent twice arrives twice.
function grow($a, $n)
{
    if ($n > 0) {
        $inner = grow($a, $n - 1);
        $a[] = "outer$n";
        return [$a, $inner];
    }
    $a[] = "base";
    return $a;
}
var_dump(grow(["start"], 2));

function tag($o, $n)
{
    if ($n > 0) {
        tag($o, $n - 1);
    }
    $o->seen[] = $n;
}
$obj = new stdClass();
$obj->seen = [];
tag($obj, 2);
var_dump($obj->seen);

function twice($p, $q, $n)
{
    if ($n > 0) {
        $p = $p + 1;
        return twice($p, $p, $n - 1);
    }
    return [$p, $q];
}
var_dump(twice(1, 0, 3));
