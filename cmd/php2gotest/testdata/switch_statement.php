<?php
// `switch` compares with `==`, and the engine has three ways of doing it.
// ZEND_IS_EQUAL when the subject is a named variable, ZEND_CASE when it is a
// **temporary** -- the comparison must not free op1, because the next case
// needs it -- and a jump table when every case is an int or every case is a
// string.

// The subject is evaluated once however the comparison is done.
function subject() { echo "evaluated once\n"; return "0"; }
switch (subject()) {
    case 0: echo "matched int 0\n"; break;
    case "0": echo "matched string 0\n"; break;
    default: echo "default\n";
}

// A temporary subject: ZEND_CASE.
$s = "abc";
switch ($s[0]) {
    case 'a': echo "a\n"; break;
    case 'b': echo "b\n"; break;
    default: echo "neither\n";
}
switch (strlen("xy")) {
    case 1: echo "one\n"; break;
    case 2: echo "two\n"; break;
}

// All-string and all-int cases: the jump table forms. The table is a **fast
// path**, so a subject of another type still reaches the sequential comparison
// and still matches loosely there.
function word($w) {
    switch ($w) {
        case "alpha": return 1;
        case "beta": return 2;
        case "gamma": return 3;
        default: return 0;
    }
}
var_dump(word("alpha"), word("gamma"), word("delta"));

function number($n) {
    switch ($n) {
        case 1: return "one";
        case 2: return "two";
        case 3: return "three";
        default: return "many";
    }
}
var_dump(number(1), number(3), number(9));
var_dump(number(1.0), number("2"), number(true));

// Fallthrough, and a default that is not last.
function fall($x) {
    $out = [];
    switch ($x) {
        case 1:
            $out[] = "one";
        case 2:
            $out[] = "two";
            break;
        default:
            $out[] = "other";
            break;
        case 3:
            $out[] = "three";
    }
    return $out;
}
var_dump(fall(1), fall(2), fall(3), fall(9));

// A switch with no matching case and no default runs nothing.
switch (99) { case 1: echo "no\n"; }
echo "done\n";
