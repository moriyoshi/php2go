<?php
// A store that takes a fresh value -- a literal, a new array, the result of a
// function that hands back an array nothing else holds -- needs no copy, and
// compiled output makes none (OWNERSHIP-PERF.md P2). Every other store must
// still give the variable its own array. Each shape below is one that would
// look like a move to a careless rule and is not.

function build($n)
{
    $a = [];
    for ($i = 0; $i < $n; $i++) {
        $a[] = $i;
    }
    return $a;
}

// Fresh each time, stored, then written: a move.
function freshThenWritten()
{
    $out = [];
    for ($i = 0; $i < 3; $i++) {
        $b = build(2);
        $b[] = $i;
        $out[] = count($b);
    }
    return [$out, count(build(2))];
}

// A method handing out a property's array: not fresh.
class Box
{
    public $items = [1];
    public function items()
    {
        return $this->items;
    }
}

function fromProperty()
{
    $box = new Box();
    $b = $box->items();
    $b[] = 2;
    return [count($b), count($box->items)];
}

// A function that keeps what it returns in a static: not fresh.
function keeper()
{
    static $kept;
    $a = [1];
    $kept = $a;
    return $a;
}

function keepsWhatItReturns()
{
    $b = keeper();
    $b[] = 2;
    $c = keeper();
    return [count($b), count($c)];
}

// A function returning its parameter: not fresh.
function id($a)
{
    return $a;
}

function throughId()
{
    $mine = [1];
    $b = id($mine);
    $b[] = 2;
    return [count($b), count($mine)];
}

// A literal, stored and written in a loop.
function literalInLoop()
{
    $out = [];
    for ($i = 0; $i < 3; $i++) {
        $b = [0];
        $b[] = $i;
        $out[] = $b;
    }
    return $out;
}

var_dump(freshThenWritten());
var_dump(fromProperty());
var_dump(keepsWhatItReturns());
var_dump(throughId());
var_dump(literalInLoop());
