<?php
// A frame reports its parameters as they stand when the trace is taken,
// not as they were passed: the engine keeps the arguments in the callee's
// own slots (bug70547, bug73156). And an exception's private `trace`
// property is built when the exception is made, so a dump shows it.

function show() {
    $bt = debug_backtrace();
    var_dump($bt[1]['function'], $bt[1]['args']);
}

// Object numbers first, before anything else is made: an exception's
// trace, and a hidden argument's wrapper, are made with the exception.
function make($x) { return new RuntimeException("made"); }
$made = make(42);
var_dump($made);
print_r($made);
echo "\n";
var_dump(count(((array)$made)["\0Exception\0trace"]));
var_dump($made->getTrace() === $made->getTrace());

// A hidden argument's wrapper is made once, with the exception.
function hidden(#[\SensitiveParameter] $secret, $shown) {
    return new LogicException("h");
}
$h = hidden("pw", "visible");
var_dump($h->getTrace()[0]['args']);
var_dump($h);
$after = new stdClass;
var_dump($after);

function written($a, $b = 7, ...$rest) {
    $a = 5;
    $b++;
    show();
    var_dump(func_get_args(), func_num_args());
    if (func_num_args() > 1) {
        var_dump(func_get_arg(1));
    }
    $rest[] = 9;            // the variadic is no argument slot
    unset($a);
    show();
    try {
        throw new Exception("x");
    } catch (Exception $e) {
        var_dump($e->getTrace()[0]['args']);
    }
}
written(1, 2, 3, 4);
written(1);

// A by-reference parameter: rebound and unset.
function byRef(&$r, $plain) {
    $r = "changed";
    show();
    unset($r);
    show();
    var_dump(func_get_args());
}
$v = "orig";
byRef($v, "p");
var_dump($v);

// Array and string parameters written in place.
function inPlace($arr, $s) {
    $arr[] = 3;
    $s .= "x";
    $s[0] = 'Q';
    show();
}
inPlace([1], "ab");

// Written only through a callee that takes it by reference.
function setIt(&$x) { $x = "set"; }
function throughCallee($p) {
    setIt($p);
    show();
}
throughCallee("before");

// Written through an eval'd string, as bug73156 does.
function viaEval($p) {
    eval('$p = "from eval";');
    show();
}
viaEval("before");

// A typed parameter written with its own type.
function typedInt(int $n, float $f) {
    $n = 5;
    $f = 2.5;
    show();
}
typedInt(1, 1.0);

// An unset parameter in a trace's string form.
function unsetThenString($a, $b) {
    unset($a);
    echo (new Exception)->getTraceAsString(), "\n";
}
unsetThenString("x", "y");

// Untouched parameters report what was passed.
function untouched($n, $m) {
    show();
}
untouched("12", "m");

// A method and a closure.
class K {
    function m($a) { $a = "m-written"; show(); }
}
(new K)->m("m-passed");
$c = function ($a) { $a = "c-written"; show(); };
$c("c-passed");

// The exception made before the write keeps the value it saw.
function early($a) {
    $e = new Exception("early");
    $a = "later";
    var_dump($e->getTrace()[0]['args']);
    show();
}
early("first");


// An unset parameter is a NULL element, not a hole.
function unsetElement($a, $b) {
    unset($a);
    $args = debug_backtrace()[0]['args'];
    var_dump(count($args), array_key_exists(0, $args), $args[0] ?? "dflt", isset($args[0]));
    foreach ($args as $k => $v) {
        var_dump($k, $v);
    }
    var_dump(array_keys(func_get_args()));
}
unsetElement(1, 2);
