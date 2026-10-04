<?php
// A by-reference generator yielding what is not a variable notices -- a
// literal, an expression, a call that returns by value -- while a call that
// returns a reference yields quietly.

function plain() { return "bar"; }
function &byRef() { static $v = "ref"; return $v; }
function &gen() {
    yield "foo";
    yield 1 + 1;
    yield plain();
    yield byRef();
}
foreach (gen() as $v) {
    var_dump($v);
}
