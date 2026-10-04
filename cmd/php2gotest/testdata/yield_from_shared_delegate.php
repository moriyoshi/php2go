<?php
// Two generators delegating to one: each reads the shared delegate's
// current element through it, so advancing it through either is seen by
// both. Once the shared one is spent -- here, advanced directly to its end
// -- a delegator keeps the element it last presented.

function inner() { yield 1; yield 2; yield 3; return 9; }
function outer($g) { $r = yield from $g; echo "outer got $r\n"; }

$shared = inner();
$a = outer($shared);
$b = outer($shared);
var_dump($a->current(), $b->current());
$a->next();
var_dump($a->current(), $b->current());
$b->next();
var_dump($a->current(), $b->current());

function one() { yield 1; }
function wrap($g) { yield from $g; }
$g1 = one();
$g2 = wrap($g1);
var_dump($g2->current());
$g1->next();
var_dump($g2->current());

// Through an intermediate generator on each side, and past the shared
// generator's own `yield from` ending so that it yields for itself again.
function nested($a = 0) {
    yield 10 + $a;
    if ($a < 1) {
        yield from nested($a + 1);
    }
    yield 20 + $a;
}
function via($g) { yield from (function () use ($g) { yield from $g; })(); }
$s = nested();
$p = via($s);
$q = via($s);
foreach ([$p, $q, $p, $q, $p, $q] as $x) {
    var_dump($x->current());
    $x->next();
}

// The shared generator yields for itself and then starts a new `yield from`:
// the other delegator sees the new source's element.
function two() { yield 30; yield 31; }
function starts() { yield 1; yield from two(); yield 2; }
$t = starts();
$u = outer($t);
$w = outer($t);
$u->current();
$w->current();
foreach ([$u, $w, $u, $w, $u, $w] as $x) {
    var_dump($x->current());
    $x->next();
}
