<?php
// A compiled call to a known function lends its arguments from the Ctx's
// argument stack instead of allocating a slice (php.Ctx.PushCallArgs,
// callUserArgs). The slots are reused as soon as the call returns, so each
// case here reads a call's arguments after that has happened.

// An exception's trace is printed long after the calls in it returned and
// later calls reused their slots.
function inner($x, $y)
{
    return new Exception("made in inner");
}
function outer($a)
{
    return inner($a * 2, "second");
}
$e = outer(21);
for ($i = 0; $i < 50; $i++) {
    inner($i, str_repeat("z", 5));
}
echo $e->getTraceAsString(), "\n";

// Deep recursion grows the argument stack while the outer frames still hold
// their slices; the trace taken at the bottom shows every level.
function down($n, $tag)
{
    if ($n == 0) {
        return (new Exception("bottom"))->getTrace();
    }
    return down($n - 1, $tag . $n);
}
$trace = down(40, "t");
echo count($trace), " ", $trace[0]["args"][1], " ", $trace[39]["args"][0], "\n";

// debug_backtrace and func_get_args read the frame's arguments during the
// call, after nested calls have come and gone.
function reads($p, $q)
{
    inner(1, 2);
    inner(3, 4);
    $bt = debug_backtrace();
    return [func_get_args(), $bt[0]["args"]];
}
var_dump(reads("p", ["q"]));

// By reference, named, defaulted and variadic.
function byRef(&$r, $v)
{
    $r = $v;
}
$target = 0;
byRef($target, "set");
var_dump($target);

function named($a, $b = "default", ...$rest)
{
    return [$a, $b, $rest, func_num_args()];
}
var_dump(named(b: 2, a: 1), named(1), named(1, 2, 3, 4));

// A generator keeps its arguments after the call that made it returned.
function gen($from, $to)
{
    for ($i = $from; $i <= $to; $i++) {
        yield $i;
    }
}
$g = gen(3, 5);
inner(9, 9);
inner(8, 8);
foreach ($g as $v) {
    echo $v, " ";
}
echo "\n";

// Its frame, re-entered on every resume, still shows the arguments it was
// called with -- in a trace taken inside it, after other calls reused slots.
function traced($first, $second)
{
    yield 1;
    yield (new Exception("inside"))->getTraceAsString();
}
$t = traced("alpha", "beta");
inner(7, 7);
foreach ($t as $v) {
    inner(6, 6);
    echo $v, "\n";
}
