<?php
// A `$` inside a string literal is **text**. A `return` of a literal lowers to
// a terminator, terminators are IR *text* rather than operands, and the
// variable rewriter scans that text -- so a single-quoted string naming a
// promoted local was rewritten to the SSA value, with an invented
// `Undefined variable` warning beside it.
//
// `PlaceNames`, which finds the same tokens for the emitter, masks quoted
// regions. Two scanners over one format that disagree about what a token is
// will corrupt whatever only one of them sees.

function named()
{
    $prop = 'unused but promoted';
    return 'B::$prop::get()';
}

function several()
{
    $a = 1;
    $b = 2;
    return '$a and $b and $undefined';
}

function escaped()
{
    $x = 'x';
    // A quote inside the literal must not look like the end of it.
    return 'he said "$x" once';
}

function interpolatedStillWorks()
{
    $who = 'world';
    // Double quotes *do* interpolate, and the engine turns that into a rope
    // before we ever see it -- so this must keep working.
    return "hello $who";
}

function dollarAlone()
{
    $v = 1;
    return 'costs $5, not $v';
}

echo named(), "\n";
echo several(), "\n";
echo escaped(), "\n";
echo interpolatedStillWorks(), "\n";
echo dollarAlone(), "\n";

// The same through a method and a closure, since each has its own body.
class Holder
{
    public function m()
    {
        $prop = 'ignored';
        return 'method sees $prop';
    }
}
echo (new Holder)->m(), "\n";

$fn = function () {
    $inner = 'ignored';
    return 'closure sees $inner';
};
echo $fn(), "\n";
