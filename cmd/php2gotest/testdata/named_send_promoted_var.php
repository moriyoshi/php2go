<?php
// A variable sent under a name to a callee the compiler cannot see -- a
// closure in a variable, a string callable, a parent hook -- reaches the
// callee with its value, whether the send is by value or may be by
// reference.

function show($a = "default a", $b = "default b") { echo "a=$a b=$b\n"; }
function bump(&$n, $by = 1) { $n += $by; }

function run() {
    $x = "X";
    $y = "Y";
    $f = 'show';
    $f(b: $x);
    $f(b: $y, a: $x);
    $g = fn($a, $b = "-") => "$a/$b";
    echo $g(b: $x, a: $y), "\n";
    $counter = 5;
    $h = 'bump';
    $h(by: 2, n: $counter);
    var_dump($counter);
    call_user_func('show', b: $x);
}
run();
