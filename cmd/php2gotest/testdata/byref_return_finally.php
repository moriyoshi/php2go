<?php
// A by-reference function returning from inside a try hands back the
// variable's reference, so a write the finally makes to it afterwards is what
// the caller sees -- as a value and when bound.

function &test() {
    try {
        return $a;
    } finally {
        $a = 2;
    }
}
var_dump(test());

function &counter() {
    static $n = 0;
    try {
        return $n;
    } finally {
        $n++;
    }
}
$r = &counter();
var_dump($r);
$r = 10;
var_dump(counter());
