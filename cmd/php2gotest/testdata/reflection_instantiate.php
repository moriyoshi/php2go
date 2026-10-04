<?php
// Instantiating and invoking through Reflection.
//
// A file of its own, with **no object temporaries before it**: php2go does not
// reuse the handle of a temporary the engine has already freed, so an object
// number printed after one differs for that reason rather than for anything
// this fixture is about. The uninitialized-property display is the point here,
// and only var_dump shows it, so the handles cannot be avoided.
// Instantiating through Reflection. `newInstanceWithoutConstructor()` is
// exactly the constructor *helper* -- declare and default every slot, run no
// `__construct` -- which is why a typed property with no default stays
// uninitialized rather than becoming null.
//
// Every instance is kept in a variable on purpose: php2go does not reuse the
// handle of a temporary the engine has already freed, so an object number
// printed after a dead temporary would differ for that reason alone.
class Plain
{
    public int $x = 5;
    private string $s = 'd';

    public function __construct()
    {
        echo "ctor\n";
    }
}

class Promoted
{
    public function __construct(public int $v)
    {
    }
}

$rPlain = new ReflectionClass('Plain');
$rPromoted = new ReflectionClass('Promoted');
$bare = $rPlain->newInstanceWithoutConstructor();
$empty = $rPromoted->newInstanceWithoutConstructor();
$made = $rPromoted->newInstance(7);
$spread = $rPromoted->newInstanceArgs([9]);
var_dump($bare, $empty, $made, $spread);

// A ReflectionFunction over a **closure** has no entry in the function table:
// its name is its own `{closure:FILE:LINE}`, the same one a trace shows.
$closure = function ($a, $b = 2) {
    return $a;
};
$rc2 = new ReflectionFunction($closure);
var_dump($rc2->getName(), $rc2->isClosure(), $rc2->invoke(5));

function plainFn($x)
{
    return $x * 2;
}

$rn = new ReflectionFunction('plainFn');
var_dump($rn->getName(), $rn->isClosure(), $rn->invoke(4), $rn->invokeArgs([6]));
