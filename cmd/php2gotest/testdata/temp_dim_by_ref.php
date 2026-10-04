<?php
// A dimension of a temporary -- a constant, an array literal, a TMP call
// result -- sent where the callee decides: by value it reads the element,
// with the read's diagnostics; by reference there is no storage to bind, and
// the engine refuses with `Cannot use temporary expression in write
// context` (gh12102, varSyntax/tempDimFetchByRefError).

function test() {
    byVal(func_get_args()[0]);
    byVal(C[1]);
    byVal(C[9]);
    byVal([7, 8][1]);
    foreach (['byRef', 'byVal'] as $f) {
        try {
            $f(C[0]);
        } catch (Error $e) {
            echo "$f: ", $e->getMessage(), "\n";
        }
    }
    try {
        byRef(func_get_args()[0]);
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
    try {
        byRef([7, 8][0]);
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
    $fn = function (&$r) { $r = 1; };
    try {
        $fn(C[0]);
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
    var_dump(C);
}

const C = ['foo', 'bar'];

function byVal($arg) {
    var_dump($arg);
}

function byRef(&$arg) {
    var_dump($arg);
}

test('y');
