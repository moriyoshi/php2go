<?php
// `func_get_args()` and `func_num_args()` are **not library functions**. The
// engine gives each its own opcode because the answer is a property of the
// *call*: what this invocation was passed. `runtime/php` has no frame to ask,
// so they are intrinsics bound to the generated unit, which holds the argument
// slice.
//
// They report what was **passed**, not what was declared: a parameter that fell
// back to its default is absent from both.

function shape($a, $b = 2, $c = 3)
{
    var_dump(func_num_args(), func_get_args());
}

shape(1);
shape(1, 20);
shape(1, 20, 30, 40);

class Host
{
    public function m()
    {
        return [func_num_args(), func_get_args()];
    }

    public static function s()
    {
        return func_get_args();
    }
}

var_dump((new Host)->m('x', 'y'));
var_dump(Host::s(1, 2, 3));

// A by-reference parameter reports its **current** value, and the write still
// reaches the caller's variable.
function byRef(&$r, $other)
{
    $r = 'written';
    return func_get_args();
}
$v = 'before';
var_dump(byRef($v, 'plain'), $v);

// Nested calls each answer about their own frame.
function outer()
{
    return [func_num_args(), inner('only')];
}
function inner()
{
    return func_num_args();
}
var_dump(outer(1, 2));

// No arguments at all.
function none() { return [func_num_args(), func_get_args()]; }
var_dump(none());

// At **global scope** there is no frame to ask, and the engine emits an
// ordinary call rather than the opcode -- so this arrives at the compiler as a
// call to a function that does not exist in the library surface and never
// could. It is a PHP Error, not a missing builtin.
try {
    func_get_args();
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    func_num_args();
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
