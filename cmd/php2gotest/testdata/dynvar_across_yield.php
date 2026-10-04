<?php
// A variable a generator or fiber creates by name survives a suspension.
function gen() {
    $name = "made";
    $$name = 1;
    yield $made;
    $$name += 10;
    yield $made;
    $other = "late";
    $$other = "x";
    yield;
    var_dump(get_defined_vars());
}
$g = gen();
foreach ($g as $v) {
    var_dump($v);
}

function viaFiber() {
    $n = "held";
    $$n = [1];
    Fiber::suspend();
    ${$n}[] = 2;
    var_dump($held);
}
$f = new Fiber('viaFiber');
$f->start();
$f->resume();

// get_defined_vars lists the declared variables, then what `$$n` created,
// in the order it did.
function listed() {
    $a = 1;
    $n = "zz";
    $$n = 2;
    $m = "yy";
    $$m = 3;
    $b = 4;
    var_dump(array_keys(get_defined_vars()));
}
listed();
