<?php
// A trace's hidden arguments are objects, and their numbers are visible: the
// engine makes a SensitiveParameterValue for each one when it builds a trace
// array and frees it with the array. debug_print_backtrace builds one and
// drops it, and `var_dump(debug_backtrace())` drops its array at the end of
// the statement -- each hands its wrappers' numbers back, last-in first-out,
// which is what the next object is numbered by.

function two(#[SensitiveParameter] $a, $plain, #[SensitiveParameter] $b)
{
    debug_print_backtrace();
    var_dump(new stdClass);
    print_r(debug_backtrace());
    var_dump(debug_backtrace());
    var_dump(new stdClass);
}

function outer($x, #[SensitiveParameter] $y)
{
    two($x, 'p', $y);
}

outer('foo', 'bar');

// A limit builds only the frames it prints.
function limited(#[SensitiveParameter] $s)
{
    inner($s);
}

function inner(#[SensitiveParameter] $t)
{
    debug_print_backtrace(0, 1);
    var_dump(new stdClass);
}

limited('secret');

// Only the wrappers go back: the receiver and an object argument belong to
// frames that are still running.
class Holder
{
    function m(#[SensitiveParameter] $s, $o)
    {
        var_dump(debug_backtrace());
        var_dump(new stdClass);
    }
}

$holder = new Holder;
$arg = new stdClass;
$holder->m('s', $arg);

// A trace that is kept keeps its wrappers: nothing is handed back.
$kept = [];
function keep($trace)
{
    global $kept;
    $kept[] = $trace;
}

function keeper(#[SensitiveParameter] $s)
{
    keep(debug_backtrace());
    $held = debug_backtrace();
    var_dump(new stdClass);
    return $held;
}

$h = keeper('x');
var_dump($h[0]['args'][0], $kept[0][0]['args'][0]);

// Ignoring arguments makes no wrappers at all.
function ignored(#[SensitiveParameter] $s)
{
    debug_print_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS);
    var_dump(debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS));
    var_dump(new stdClass);
}

ignored('x');

// An eval frame has no `args` key; an include frame has one only when it
// names the file, placed before `function`, and keeps it when arguments are
// ignored.
function shown($y)
{
    var_dump(debug_backtrace());
    var_dump(debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS)[1]);
    debug_print_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS);
    echo (new Exception)->getTraceAsString(), "\n";
    var_dump(count((new Exception)->getTrace()[1]));
}

function includer($x)
{
    include __DIR__ . '/sensitive_trace_handles.inc';
    eval('shown(6);');
}

includer(1);
